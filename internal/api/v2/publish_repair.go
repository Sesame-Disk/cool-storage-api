package v2

import (
	"encoding/json"
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
	publishedBlockReferenceRepairPreCASLease   = 5 * time.Minute
	publishedBlockReferenceRepairRetryBase     = 5 * time.Minute
	publishedBlockReferenceRepairRetryMax      = 6 * time.Hour
	pendingPublishedFSObjectOwnerStaleAfter    = 24 * time.Hour
	pendingPublishedFSObjectOwnerSweepInterval = 15 * time.Minute
	pendingPublishedFSObjectOwnerLookbackDays  = db.PendingPublishedFSObjectOwnerTTLSeconds / (24 * 60 * 60)
)

type publishedBlockReferenceRepair struct {
	Bucket         int
	OrgID          string
	RepoID         string
	CommitID       string
	FSID           string
	StagedBlockIDs []string
	CreatedAt      time.Time
	// LeaseExpiresAt remains persisted for scheduling/diagnostics compatibility;
	// it is never publication authority and never authorizes cleanup.
	LeaseExpiresAt time.Time
}

// publishedBlockReferenceRepairCommitOutcome is deliberately fail-closed.
// Only positive reachability is actionable in the background repair path.
// A false or incomplete reachability observation may be caused by a stale,
// locally blind, or otherwise ambiguous view of the canonical publication.
// It must retain the durable row and all artifacts for a later confirmation.
type publishedBlockReferenceRepairCommitOutcome uint8

const (
	publishedBlockReferenceRepairCommitUnknown publishedBlockReferenceRepairCommitOutcome = iota
	publishedBlockReferenceRepairCommitReachable
)

var scheduledPublishedBlockReferenceRepairs sync.Map

// Retry backoff is process-local advisory state. The durable repair row is
// deliberately written and deleted only with ordinary mutations; it is not a
// Paxos state machine. Losing this hint on restart is safe: the next sweep may
// retry the durable row earlier, but cleanup authority remains unchanged.
var publishedBlockReferenceRepairNextRetryAt sync.Map

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
	if err := database.Session().Query(`
		INSERT INTO published_block_reference_repairs (bucket, org_id, repo_id, commit_id, fs_id, staged_block_ids, created_at, lease_expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, repair.Bucket, repair.OrgID, repair.RepoID, repair.CommitID, repair.FSID, repair.StagedBlockIDs, repair.CreatedAt, repair.LeaseExpiresAt).Exec(); err != nil {
		return err
	}
	publishedBlockReferenceRepairNextRetryAt.Delete(publishedBlockReferenceRepairRetryKey(repair))
	return nil
}

var deletePublishedBlockReferenceRepairFn = func(database *db.DB, repair publishedBlockReferenceRepair) error {
	if database == nil {
		return fmt.Errorf("database not available")
	}
	if err := database.Session().Query(`
		DELETE FROM published_block_reference_repairs
		WHERE bucket = ? AND org_id = ? AND repo_id = ? AND commit_id = ? AND fs_id = ?
	`, repair.Bucket, repair.OrgID, repair.RepoID, repair.CommitID, repair.FSID).
		Exec(); err != nil {
		return err
	}
	publishedBlockReferenceRepairNextRetryAt.Delete(publishedBlockReferenceRepairRetryKey(repair))
	return nil
}

// schedulePublishedBlockReferenceRepairRetryFn records only process-local
// advisory backoff. It intentionally does not mutate the durable repair row:
// restart may forget this hint, and a retry can never resurrect a settled row.
var schedulePublishedBlockReferenceRepairRetryFn = func(database *db.DB, repair publishedBlockReferenceRepair, nextRetryAt time.Time) error {
	if database == nil {
		return fmt.Errorf("database not available")
	}
	if nextRetryAt.IsZero() {
		nextRetryAt = publishedBlockReferenceRepairNowFn().UTC()
	}
	publishedBlockReferenceRepairNextRetryAt.Store(publishedBlockReferenceRepairRetryKey(repair), nextRetryAt.UTC())
	return nil
}

var listPublishedBlockReferenceRepairsForBucketFn = func(database *db.DB, bucket int) ([]publishedBlockReferenceRepair, error) {
	if database == nil {
		return nil, fmt.Errorf("database not available")
	}
	iter := database.Session().Query(`
		SELECT org_id, repo_id, commit_id, fs_id, staged_block_ids, created_at, lease_expires_at
		FROM published_block_reference_repairs WHERE bucket = ?
	`, bucket).Iter()

	var repairs []publishedBlockReferenceRepair
	var repair publishedBlockReferenceRepair
	for iter.Scan(&repair.OrgID, &repair.RepoID, &repair.CommitID, &repair.FSID, &repair.StagedBlockIDs, &repair.CreatedAt, &repair.LeaseExpiresAt) {
		repair.Bucket = bucket
		repairs = append(repairs, repair)
		repair = publishedBlockReferenceRepair{}
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return repairs, nil
}

var listPendingPublishedFSObjectOwnersByDayFn = func(database *db.DB, day time.Time, bucket int) ([]db.PendingPublishedFSObjectOwner, error) {
	if database == nil {
		return nil, fmt.Errorf("database not available")
	}
	return database.ListPendingPublishedFSObjectOwnersByDay(day, bucket)
}

var loadPendingPublishedFSObjectOwnerFn = func(database *db.DB, repoID, fsID, ownerID string) (db.PendingPublishedFSObjectOwner, error) {
	if database == nil {
		return db.PendingPublishedFSObjectOwner{}, fmt.Errorf("database not available")
	}
	return database.LoadPendingPublishedFSObjectOwner(repoID, fsID, ownerID)
}

var publishedBlockReferenceRepairHeadCommitFn = func(database *db.DB, orgID, repoID string) (string, error) {
	if database == nil {
		return "", fmt.Errorf("database not available")
	}
	var headCommitID string
	err := database.Session().Query(`
		SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Consistency(gocql.Serial).Scan(&headCommitID)
	if err != nil {
		return "", fmt.Errorf("lookup canonical HEAD for repo %s: %w", repoID, err)
	}
	if strings.TrimSpace(headCommitID) == "" {
		return "", fmt.Errorf("canonical HEAD for repo %s is empty", repoID)
	}
	return strings.TrimSpace(headCommitID), nil
}

var publishedBlockReferenceRepairCommitParentFn = func(database *db.DB, repoID, commitID string) (string, error) {
	if database == nil {
		return "", fmt.Errorf("database not available")
	}
	var parentCommitID string
	err := database.Session().Query(`
		SELECT parent_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, commitID).Consistency(gocql.EachQuorum).Scan(&parentCommitID)
	if err != nil {
		return "", err
	}
	return parentCommitID, nil
}

func classifyPublishedBlockReferenceRepairCommitOutcome(commitID, headCommitID string, parentLookup func(string) (string, error)) (publishedBlockReferenceRepairCommitOutcome, error) {
	commitID = strings.TrimSpace(commitID)
	headCommitID = strings.TrimSpace(headCommitID)
	if commitID == "" || headCommitID == "" {
		return publishedBlockReferenceRepairCommitUnknown, fmt.Errorf("commit and canonical HEAD are required to settle publication")
	}

	reachable, err := onlyOfficeCommitReachable(commitID, headCommitID, parentLookup)
	if err != nil {
		return publishedBlockReferenceRepairCommitUnknown, fmt.Errorf("resolve commit %s reachability: %w", commitID, err)
	}
	if reachable {
		return publishedBlockReferenceRepairCommitReachable, nil
	}

	return publishedBlockReferenceRepairCommitUnknown, nil
}

var publishedBlockReferenceRepairCommitReachableFn = func(database *db.DB, orgID, repoID, commitID string) (publishedBlockReferenceRepairCommitOutcome, error) {
	headCommitID, err := publishedBlockReferenceRepairHeadCommitFn(database, orgID, repoID)
	if err != nil {
		return publishedBlockReferenceRepairCommitUnknown, fmt.Errorf("lookup current head for repo %s: %w", repoID, err)
	}
	return classifyPublishedBlockReferenceRepairCommitOutcome(commitID, headCommitID, func(currentCommitID string) (string, error) {
		parentCommitID, err := publishedBlockReferenceRepairCommitParentFn(database, repoID, currentCommitID)
		if err != nil {
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

var cleanupFailedPublishDeleteCommitFn = func(database *db.DB, repoID, commitID string) error {
	if database == nil {
		return fmt.Errorf("database not available")
	}
	return database.Session().Query(`
		DELETE FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, commitID).Exec()
}

var cleanupFailedPublishRemoveAttemptReferencesFn = db.RemovePublishAttemptReferences

var cleanupFailedPublishDeleteFSObjectFn = func(database *db.DB, repoID, fsID string) error {
	if database == nil {
		return fmt.Errorf("database not available")
	}
	return database.Session().Query(`
		DELETE FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, fsID).Exec()
}

var cleanupFailedPublishDeletePendingOwnerFn = func(database *db.DB, repoID, fsID, ownerID string, createdAt time.Time) error {
	if database == nil {
		return fmt.Errorf("database not available")
	}
	return database.DeletePendingPublishedFSObjectOwner(repoID, fsID, ownerID, createdAt)
}

var cleanupFailedPublishPendingOwnerExistsFn = func(database *db.DB, repoID, fsID string) (bool, error) {
	if database == nil {
		return false, fmt.Errorf("database not available")
	}
	return database.PendingPublishedFSObjectOwnerExists(repoID, fsID)
}

var cleanupFailedPublishFSObjectReachableFn = func(database *db.DB, repoID, fsID string) (bool, error) {
	return failedPublishFSObjectReachable(database, repoID, fsID)
}

var failedPublishReachabilityLoadFSObjectFn = func(database *db.DB, repoID, fsID string) (string, string, error) {
	if database == nil {
		return "", "", fmt.Errorf("database not available")
	}
	var objType, dirEntriesJSON string
	err := database.Session().Query(`
			SELECT obj_type, dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
		`, repoID, fsID).Scan(&objType, &dirEntriesJSON)
	return objType, dirEntriesJSON, err
}

var clearPendingPublishedFileOwnersFn = clearPendingPublishedFileOwners

var clearPendingPublishedFileOwnerFn = clearPendingPublishedFileOwner

var releasePendingPublishedFileOwnersFn = releasePendingPublishedFileOwners

var releasePendingPublishedFileOwnerFn = releasePendingPublishedFileOwner

var cleanupPendingPublishedFileOwnerAttemptFn = cleanupPendingPublishedFileOwnerAttempt

var cleanupPendingPublishedFileAttemptCommitReachableFn = publishedBlockReferenceRepairCommitReachableFn

var pendingPublishedFSObjectOwnerNowFn = time.Now

var publishedBlockReferenceRepairPromoteFn = func(helper *FSHelper, orgID, repoID, commitID string, pending *pendingPublishedFile) error {
	return helper.promotePendingPublishedFiles(orgID, repoID, commitID, []*pendingPublishedFile{pending})
}

func CleanupFailedPublishArtifacts(database *db.DB, orgID, repoID, attemptID, commitID string, fsIDs, blockIDs []string) error {
	if database == nil {
		return nil
	}
	var cleanupErr error
	commitID = strings.TrimSpace(commitID)
	if commitID != "" {
		if err := cleanupFailedPublishDeleteCommitFn(database, repoID, commitID); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete failed publish commit %s: %w", commitID, err))
		}
	}
	attemptID = strings.TrimSpace(attemptID)
	blockIDs = db.NormalizeBlockIDs(blockIDs)
	if attemptID != "" && len(blockIDs) > 0 {
		if err := cleanupFailedPublishRemoveAttemptReferencesFn(database, orgID, attemptID, blockIDs); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove publish-attempt refs for %s: %w", attemptID, err))
		}
	}
	return cleanupErr
}

func CleanupFailedPublishAttempt(database *db.DB, orgID, repoID, attemptID, commitID string, pendingFiles []*pendingPublishedFile) error {
	if err := CleanupFailedPublishArtifacts(database, orgID, repoID, attemptID, commitID, pendingPublishedFileFSIDs(pendingFiles), pendingPublishedFileInternalBlockIDs(pendingFiles)); err != nil {
		return err
	}
	return releasePendingPublishedFileOwnersFn(database, repoID, pendingFiles)
}

func clearPendingPublishedFileOwners(database *db.DB, repoID string, pendingFiles []*pendingPublishedFile) error {
	if database == nil || strings.TrimSpace(repoID) == "" {
		return nil
	}
	var clearErr error
	for _, pending := range pendingFiles {
		if err := clearPendingPublishedFileOwnerFn(database, repoID, pending); err != nil {
			clearErr = errors.Join(clearErr, err)
		}
	}
	return clearErr
}

func clearPendingPublishedFileOwner(database *db.DB, repoID string, pending *pendingPublishedFile) error {
	if database == nil || pending == nil {
		return nil
	}
	fsID := strings.TrimSpace(pending.fsID)
	ownerID := strings.TrimSpace(pending.cleanupOwnerID)
	if fsID == "" || ownerID == "" {
		return nil
	}
	if err := cleanupFailedPublishDeletePendingOwnerFn(database, repoID, fsID, ownerID, pending.cleanupCreatedAt); err != nil {
		return fmt.Errorf("clear pending publish owner for fs_object %s: %w", fsID, err)
	}
	return nil
}

func releasePendingPublishedFileOwners(database *db.DB, repoID string, pendingFiles []*pendingPublishedFile) error {
	if database == nil || strings.TrimSpace(repoID) == "" {
		return nil
	}
	var releaseErr error
	for _, pending := range pendingFiles {
		if err := releasePendingPublishedFileOwnerFn(database, repoID, pending); err != nil {
			releaseErr = errors.Join(releaseErr, err)
		}
	}
	return releaseErr
}

func releasePendingPublishedFileOwner(database *db.DB, repoID string, pending *pendingPublishedFile) error {
	if database == nil || pending == nil {
		return nil
	}
	fsID := strings.TrimSpace(pending.fsID)
	ownerID := strings.TrimSpace(pending.cleanupOwnerID)
	if fsID == "" || ownerID == "" {
		return nil
	}
	if err := cleanupFailedPublishDeletePendingOwnerFn(database, repoID, fsID, ownerID, pending.cleanupCreatedAt); err != nil {
		return fmt.Errorf("release pending publish owner for fs_object %s: %w", fsID, err)
	}
	// fs_id is content-addressed and can be shared by concurrent publish attempts.
	// Removing fs_objects here can delete the metadata row another owner just began using.
	return nil
}

func ReleasePendingPublishedFSObjectOwner(database *db.DB, repoID, fsID, ownerID string, createdAt time.Time) error {
	return releasePendingPublishedFileOwner(database, repoID, &pendingPublishedFile{
		fsID:             fsID,
		cleanupOwnerID:   ownerID,
		cleanupCreatedAt: createdAt,
	})
}

func ClearPendingPublishedFSObjectOwner(database *db.DB, repoID, fsID, ownerID string, createdAt time.Time) error {
	return clearPendingPublishedFileOwner(database, repoID, &pendingPublishedFile{
		fsID:             fsID,
		cleanupOwnerID:   ownerID,
		cleanupCreatedAt: createdAt,
	})
}

func cleanupPendingPublishedFileOwnerAttempt(database *db.DB, repoID string, pending *pendingPublishedFile) error {
	if database == nil || pending == nil {
		return nil
	}
	fsID := strings.TrimSpace(pending.fsID)
	attemptID := strings.TrimSpace(pending.cleanupAttemptID)
	if attemptID == "" {
		return fmt.Errorf("pending publish owner for fs_object %s is missing cleanup attempt metadata", fsID)
	}
	orgID := strings.TrimSpace(pending.cleanupOrgID)
	if orgID == "" {
		return fmt.Errorf("pending publish owner for fs_object %s is missing cleanup org_id", fsID)
	}
	outcome, err := cleanupPendingPublishedFileAttemptCommitReachableFn(database, orgID, repoID, attemptID)
	if err != nil {
		return fmt.Errorf("check publish attempt commit %s reachability for fs_object %s: %w", attemptID, fsID, err)
	}
	switch outcome {
	case publishedBlockReferenceRepairCommitReachable:
		promotePending, err := loadPublishedBlockReferenceRepairPendingFileFn(database, repoID, fsID)
		if err != nil {
			return fmt.Errorf("load reachable published fs_object %s for commit %s: %w", fsID, attemptID, err)
		}
		promotePending.cleanupOwnerID = pending.cleanupOwnerID
		promotePending.cleanupCreatedAt = pending.cleanupCreatedAt
		promotePending.cleanupOrgID = orgID
		promotePending.cleanupAttemptID = attemptID
		promotePending.internalBlockIDs = append([]string(nil), db.NormalizeBlockIDs(pending.internalBlockIDs)...)
		helper := NewFSHelper(database)
		if err := publishedBlockReferenceRepairPromoteFn(helper, orgID, repoID, attemptID, promotePending); err != nil {
			return fmt.Errorf("promote reachable published fs_object %s for commit %s: %w", fsID, attemptID, err)
		}
		return clearPendingPublishedFileOwnerFn(database, repoID, pending)
	default:
		return fmt.Errorf("publication outcome for commit %s is unknown; retain pending fs_object owner", attemptID)
	}
}

func failedPublishFSObjectReachable(database *db.DB, repoID, targetFSID string) (bool, error) {
	if database == nil || strings.TrimSpace(repoID) == "" || strings.TrimSpace(targetFSID) == "" {
		return false, nil
	}
	iter := database.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ?
	`, repoID).Iter()
	visited := make(map[string]bool)
	var rootFSID string
	for iter.Scan(&rootFSID) {
		reachable, err := failedPublishFSObjectReachableFromRoot(database, repoID, targetFSID, rootFSID, visited)
		if err != nil {
			_ = iter.Close()
			return false, err
		}
		if reachable {
			if err := iter.Close(); err != nil {
				return false, fmt.Errorf("close commit iterator for repo %s: %w", repoID, err)
			}
			return true, nil
		}
	}
	if err := iter.Close(); err != nil {
		return false, fmt.Errorf("list commits for repo %s: %w", repoID, err)
	}
	return false, nil
}

func failedPublishFSObjectReachableFromRoot(database *db.DB, repoID, targetFSID, rootFSID string, visited map[string]bool) (bool, error) {
	if rootFSID == "" {
		return false, nil
	}
	stack := []string{rootFSID}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current == "" || visited[current] {
			continue
		}
		if current == targetFSID {
			return true, nil
		}
		visited[current] = true

		objType, dirEntriesJSON, err := failedPublishReachabilityLoadFSObjectFn(database, repoID, current)
		if err != nil {
			if errors.Is(err, gocql.ErrNotFound) {
				continue
			}
			return false, fmt.Errorf("load fs_object %s in repo %s: %w", current, repoID, err)
		}
		if objType != "dir" {
			continue
		}
		var entries []FSEntry
		if dirEntriesJSON != "" && dirEntriesJSON != "[]" {
			if err := json.Unmarshal([]byte(dirEntriesJSON), &entries); err != nil {
				return false, fmt.Errorf("decode dir_entries for fs_object %s in repo %s: %w", current, repoID, err)
			}
		}
		for i := len(entries) - 1; i >= 0; i-- {
			if childID := strings.TrimSpace(entries[i].ID); childID != "" && !visited[childID] {
				stack = append(stack, childID)
			}
		}
	}
	return false, nil
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
	now := publishedBlockReferenceRepairNowFn().UTC()
	return publishedBlockReferenceRepair{
		Bucket:         publishedBlockReferenceRepairBucket(orgID, repoID, commitID, fsID),
		OrgID:          orgID,
		RepoID:         repoID,
		CommitID:       commitID,
		FSID:           fsID,
		StagedBlockIDs: append([]string(nil), db.NormalizeBlockIDs(stagedBlockIDs)...),
		CreatedAt:      now,
		LeaseExpiresAt: now.Add(publishedBlockReferenceRepairPreCASLease),
	}
}

// publishedBlockReferenceRepairRetryDelay is deliberately derived from row
// age rather than an unbounded retry counter. That keeps the existing schema,
// makes the delay monotonic for a permanently ambiguous row, and caps the
// expensive ancestry walk at a predictable rate.
func publishedBlockReferenceRepairRetryDelay(now, createdAt time.Time) time.Duration {
	delay := now.Sub(createdAt)
	if delay < publishedBlockReferenceRepairRetryBase {
		return publishedBlockReferenceRepairRetryBase
	}
	if delay > publishedBlockReferenceRepairRetryMax {
		return publishedBlockReferenceRepairRetryMax
	}
	return delay
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

func publishedBlockReferenceRepairRetryKey(repair publishedBlockReferenceRepair) string {
	return strings.TrimSpace(repair.OrgID) + `:` + publishedBlockReferenceRepairKey(repair.RepoID, repair.CommitID, repair.FSID)
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

func repairPublishedBlockReferenceRepair(database *db.DB, repair publishedBlockReferenceRepair) error {
	if !shouldQueuePublishedBlockReferenceRepair(repair.FSID, repair.StagedBlockIDs) || strings.TrimSpace(repair.CommitID) == "" {
		return nil
	}
	if len(repair.StagedBlockIDs) == 0 {
		return fmt.Errorf("queued publish repair for fs_object %s has no staged block IDs", repair.FSID)
	}
	commitOutcome, err := publishedBlockReferenceRepairCommitReachableFn(database, repair.OrgID, repair.RepoID, repair.CommitID)
	if err != nil {
		return err
	}
	switch commitOutcome {
	case publishedBlockReferenceRepairCommitReachable:
		pending, err := loadPublishedBlockReferenceRepairPendingFileFn(database, repair.RepoID, repair.FSID)
		if err != nil {
			return err
		}
		pending.internalBlockIDs = append([]string(nil), repair.StagedBlockIDs...)
		helper := NewFSHelper(database)
		if err := publishedBlockReferenceRepairPromoteFn(helper, repair.OrgID, repair.RepoID, repair.CommitID, pending); err != nil {
			return fmt.Errorf("promote published fs_object %s for commit %s: %w", repair.FSID, repair.CommitID, err)
		}
	default:
		return fmt.Errorf("publication outcome for fs_object %s commit %s is unknown; retain queued repair", repair.FSID, repair.CommitID)
	}
	if err := deletePublishedBlockReferenceRepairFn(database, repair); err != nil {
		return fmt.Errorf("delete queued publish repair for fs_object %s: %w", repair.FSID, err)
	}
	return nil
}

func runPendingPublishedFSObjectOwnerSweep(database *db.DB) error {
	if database == nil {
		return nil
	}
	now := pendingPublishedFSObjectOwnerNowFn().UTC()
	cutoff := now.Add(-pendingPublishedFSObjectOwnerStaleAfter)
	startDay := db.GCProjectionUTCDate(now.AddDate(0, 0, -pendingPublishedFSObjectOwnerLookbackDays))
	endDay := db.GCProjectionUTCDate(now)
	var firstErr error
	for day := startDay; !day.After(endDay); day = day.AddDate(0, 0, 1) {
		for bucket := 0; bucket < db.GCDiscoveryBucketCount; bucket++ {
			owners, err := listPendingPublishedFSObjectOwnersByDayFn(database, day, bucket)
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("list pending published fs_object owners for day=%s bucket=%d: %w", db.GCProjectionDateString(day), bucket, err)
				}
				continue
			}
			for _, owner := range owners {
				if owner.CreatedAt.IsZero() || owner.CreatedAt.After(cutoff) {
					continue
				}
				if strings.TrimSpace(owner.AttemptID) == "" {
					loadedOwner, err := loadPendingPublishedFSObjectOwnerFn(database, owner.RepoID, owner.FSID, owner.OwnerID)
					if err != nil {
						if errors.Is(err, gocql.ErrNotFound) {
							if deleteErr := cleanupFailedPublishDeletePendingOwnerFn(database, owner.RepoID, owner.FSID, owner.OwnerID, owner.CreatedAt); deleteErr != nil {
								log.Printf("[publish_repair] failed to delete dangling pending fs_object owner projection repo=%s fs_object=%s owner=%s: %v", owner.RepoID, owner.FSID, owner.OwnerID, deleteErr)
								if firstErr == nil {
									firstErr = deleteErr
								}
							}
							continue
						}
						log.Printf("[publish_repair] failed to hydrate pending fs_object owner repo=%s fs_object=%s owner=%s: %v", owner.RepoID, owner.FSID, owner.OwnerID, err)
						if firstErr == nil {
							firstErr = err
						}
						continue
					}
					if !loadedOwner.CreatedAt.IsZero() {
						owner.CreatedAt = loadedOwner.CreatedAt
					}
					owner.OrgID = loadedOwner.OrgID
					owner.AttemptID = loadedOwner.AttemptID
					owner.BlockIDs = append([]string(nil), loadedOwner.BlockIDs...)
				}
				pending := &pendingPublishedFile{
					fsID:             owner.FSID,
					internalBlockIDs: append([]string(nil), owner.BlockIDs...),
					cleanupOwnerID:   owner.OwnerID,
					cleanupCreatedAt: owner.CreatedAt,
					cleanupOrgID:     owner.OrgID,
					cleanupAttemptID: owner.AttemptID,
				}
				if err := cleanupPendingPublishedFileOwnerAttemptFn(database, owner.RepoID, pending); err != nil {
					log.Printf("[publish_repair] stale pending fs_object owner cleanup failed for repo=%s fs_object=%s owner=%s: %v", owner.RepoID, owner.FSID, owner.OwnerID, err)
					if firstErr == nil {
						firstErr = err
					}
				}
			}
		}
	}
	return firstErr
}

func shouldRunPendingPublishedFSObjectOwnerSweep(lastRun, now time.Time) bool {
	return lastRun.IsZero() || !now.Before(lastRun.Add(pendingPublishedFSObjectOwnerSweepInterval))
}

func RepairPublishedFSObjectBlockReferenceRepair(database *db.DB, orgID, repoID, commitID, fsID string, stagedBlockIDs []string) error {
	return repairPublishedBlockReferenceRepair(database, newPublishedBlockReferenceRepair(orgID, repoID, commitID, fsID, stagedBlockIDs))
}

func runPublishedBlockReferenceRepairSweep(database *db.DB) error {
	if database == nil {
		return nil
	}
	now := publishedBlockReferenceRepairNowFn().UTC()
	cutoff := now.Add(-publishedBlockReferenceRepairStaleAfter)
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
			retryKey := publishedBlockReferenceRepairRetryKey(repair)
			if nextRetry, ok := publishedBlockReferenceRepairNextRetryAt.Load(retryKey); ok {
				if retryAt, ok := nextRetry.(time.Time); ok && retryAt.After(now) {
					continue
				}
				if _, ok := nextRetry.(time.Time); !ok {
					publishedBlockReferenceRepairNextRetryAt.Delete(retryKey)
				}
			}
			if !repair.CreatedAt.IsZero() && repair.CreatedAt.After(cutoff) {
				continue
			}
			// lease_expires_at is advisory scheduling state only. It is not
			// consulted by the settlement function and never authorizes cleanup.
			if !repair.LeaseExpiresAt.IsZero() && repair.LeaseExpiresAt.After(now) {
				continue
			}
			if err := repairPublishedBlockReferenceRepair(database, repair); err != nil {
				nextRetryAt := now.Add(publishedBlockReferenceRepairRetryDelay(now, repair.CreatedAt))
				if retryErr := schedulePublishedBlockReferenceRepairRetryFn(database, repair, nextRetryAt); retryErr != nil {
					err = errors.Join(err, fmt.Errorf("schedule next publish repair retry at %s: %w", nextRetryAt.Format(time.RFC3339), retryErr))
				}
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
			var lastOwnerSweepAt time.Time
			runOwnerSweep := func() {
				if err := runPendingPublishedFSObjectOwnerSweep(database); err != nil {
					log.Printf("[publish_repair] pending fs_object owner sweep failed: %v", err)
				}
				lastOwnerSweepAt = pendingPublishedFSObjectOwnerNowFn().UTC()
			}
			if err := runPublishedBlockReferenceRepairSweep(database); err != nil {
				log.Printf("[publish_repair] initial sweep failed: %v", err)
			}
			runOwnerSweep()
			ticker := publishedBlockReferenceRepairTickerFn(publishedBlockReferenceRepairSweepInterval)
			defer ticker.Stop()
			for range ticker.C {
				if err := runPublishedBlockReferenceRepairSweep(database); err != nil {
					log.Printf("[publish_repair] periodic sweep failed: %v", err)
				}
				if shouldRunPendingPublishedFSObjectOwnerSweep(lastOwnerSweepAt, pendingPublishedFSObjectOwnerNowFn().UTC()) {
					runOwnerSweep()
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
