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
	// LeaseExpiresAt is retained in the repair schema for compatibility with
	// queued rows, but it is metadata only. It never authorizes cleanup.
	publishedBlockReferenceRepairPreCASLease  = 5 * time.Minute
	pendingPublishedFSObjectOwnerStaleAfter   = 24 * time.Hour
	pendingPublishedFSObjectOwnerLookbackDays = db.PendingPublishedFSObjectOwnerTTLSeconds / (24 * 60 * 60)
)

type publishedBlockReferenceRepair struct {
	Bucket         int
	OrgID          string
	RepoID         string
	CommitID       string
	FSID           string
	StagedBlockIDs []string
	CreatedAt      time.Time
	LeaseExpiresAt time.Time
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
		INSERT INTO published_block_reference_repairs (bucket, org_id, repo_id, commit_id, fs_id, staged_block_ids, created_at, lease_expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, repair.Bucket, repair.OrgID, repair.RepoID, repair.CommitID, repair.FSID, repair.StagedBlockIDs, repair.CreatedAt, repair.LeaseExpiresAt).Exec()
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

// publishedBlockReferenceRepairHeadCommitFn resolves HEAD via a SERIAL read
// (see FSHelper.getCanonicalHeadCommitSerial), not the ordinary LOCAL_QUORUM
// read used elsewhere. It also observes publication_state in that same
// authority domain. A missing libraries row is not enough to authorize repair;
// the caller must separately find the durable revocation witness.
var publishedBlockReferenceRepairHeadCommitFn = func(database *db.DB, orgID, repoID string) (string, error) {
	if database == nil {
		return "", fmt.Errorf("database not available")
	}
	fsHelper := NewFSHelper(database)
	return fsHelper.getCanonicalHeadCommitSerial(orgID, repoID)
}

var publishedBlockReferenceRepairTerminalFn = func(database *db.DB, orgID, repoID string) (bool, error) {
	if database == nil {
		return false, fmt.Errorf("database not available")
	}
	return db.IsLibraryPublicationRevoked(database.Session(), orgID, repoID)
}

// publishedBlockReferenceRepairCommitParentFn reads at EACH_QUORUM, not the
// ordinary session consistency (LOCAL_QUORUM) commits are written at: this
// ancestry walk backs the same irreversible cleanup decision
// getCanonicalHeadCommitSerial's SERIAL HEAD read protects, and a plain
// LOCAL_QUORUM read of a DIFFERENT DC than the one that inserted a given
// commit row can miss it entirely during replication lag -- the same
// LOCAL_QUORUM-write/LOCAL_QUORUM-read non-intersection gap X2 closed for
// block_references. Because the write's own home DC already has the row at
// its own local quorum, an EACH_QUORUM read (a quorum in EVERY DC) is
// guaranteed to intersect it regardless of which DC serves the read, without
// needing SERIAL: commits are ordinary immutable inserts, not the LWT value
// being linearized.
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

// publishedBlockReferenceRepairCommitReachableFn resolves three distinct
// verdicts, not a plain bool: reachable (promote), confirmed terminal
// publication authority (safe to clean up), or neither (the commit is not
// currently reachable from a live library's HEAD, which proves nothing about
// whether its writer is still going to CAS onto it). Only the first two
// verdicts authorize repairPublishedBlockReferenceRepair to act. A missing
// libraries row is terminal only when the never-deleted revocation witness is
// visible at EACH_QUORUM.
var publishedBlockReferenceRepairCommitReachableFn = func(database *db.DB, orgID, repoID, commitID string) (reachable bool, libraryGone bool, err error) {
	headCommitID, err := publishedBlockReferenceRepairHeadCommitFn(database, orgID, repoID)
	if err != nil {
		if errors.Is(err, db.ErrLibraryPublicationTerminal) {
			return false, true, nil
		}
		if errors.Is(err, db.ErrLibraryDeleted) {
			// Soft deletion is not terminal publication revocation. The library
			// can still be restored, so retain the repair row and its pub: refs.
			return false, false, nil
		}
		if errors.Is(err, gocql.ErrNotFound) {
			revoked, revokeErr := publishedBlockReferenceRepairTerminalFn(database, orgID, repoID)
			if revokeErr != nil {
				return false, false, fmt.Errorf("confirm terminal publication authority for repo %s: %w", repoID, revokeErr)
			}
			if revoked {
				return false, true, nil
			}
			// Absence is not revocation. Keep the row for a later pass rather
			// than turning a replication gap or an old deployment into cleanup.
			return false, false, nil
		}
		return false, false, fmt.Errorf("lookup current head for repo %s: %w", repoID, err)
	}
	reachable, err = onlyOfficeCommitReachable(commitID, headCommitID, func(currentCommitID string) (string, error) {
		parentCommitID, err := publishedBlockReferenceRepairCommitParentFn(database, repoID, currentCommitID)
		if err != nil {
			// Deliberately NOT swallowing ErrNotFound into ("", nil) here.
			// onlyOfficeCommitReachable treats an empty parent as the
			// legitimate end of a chain (a real root commit, whose own row
			// EXISTS with parent_id=""). This walk only ever visits commit
			// IDs an earlier authority (HEAD, or another commit's own
			// parent_id) already told us belong to the chain, so "row not
			// found here" means "cannot prove this ancestor's parent," never
			// "this ancestor has no parent" -- conflating the two would let
			// a commit row that simply has not replicated to this DC yet
			// masquerade as a root and truncate the walk, producing the same
			// false-unreachable verdict this whole check exists to prevent.
			// Propagating the error instead makes repairPublishedBlockReferenceRepair
			// keep the repair row and retry on the next sweep.
			return "", fmt.Errorf("lookup parent for commit %s: %w", currentCommitID, err)
		}
		return parentCommitID, nil
	})
	return reachable, false, err
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

// publishedBlockReferenceRepairCleanupFn deliberately passes "" as
// CleanupFailedPublishArtifacts' commitID, never deleting a commits row. It
// is called only after terminal publication authority has been confirmed, so
// removing the staged pub: reference is safe: no writer can still win the HEAD
// CAS. The commit row is retained as inert historical debris; inferred repair
// must not delete it.
var publishedBlockReferenceRepairCleanupFn = func(database *db.DB, orgID, repoID, commitID, fsID string, blockIDs []string) error {
	return CleanupFailedPublishArtifacts(database, orgID, repoID, commitID, "", []string{fsID}, blockIDs)
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
	reachable, libraryGone, err := cleanupPendingPublishedFileAttemptCommitReachableFn(database, orgID, repoID, attemptID)
	if err != nil {
		return fmt.Errorf("check publish attempt commit %s reachability for fs_object %s: %w", attemptID, fsID, err)
	}
	if reachable {
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
	}
	if !libraryGone {
		// Unreachable from the current live HEAD, but the library itself is
		// not confirmed gone: same reasoning as repairPublishedBlockReferenceRepair
		// below -- an owner record's age alone (even the 24h staleness this
		// sweep requires) proves nothing about whether the writer that
		// created it can still complete its own HEAD CAS. Leave it for a
		// future sweep pass rather than release/destroy on an inference.
		return nil
	}
	if err := cleanupPendingPublishedFileAttemptArtifacts(database, repoID, pending); err != nil {
		return err
	}
	return releasePendingPublishedFileOwner(database, repoID, pending)
}

// cleanupPendingPublishedFileAttemptArtifacts is only reached once
// cleanupPendingPublishedFileOwnerAttempt has confirmed libraryGone==true, so
// unlike publishedBlockReferenceRepairCleanupFn's caller it never runs
// against a library that might still have a live writer. It still never
// passes attemptID as the commitID to delete, though: matching
// publishedBlockReferenceRepairCleanupFn keeps a single rule for every
// inferred (not same-process, self-directed) cleanup path in this file --
// "never delete commits" -- rather than two paths that agree only some of
// the time. An orphaned commits row is inert either way (nothing walks it
// except from a HEAD-reachable ancestry chain), and HardDeleteLibrary
// (internal/gc/store_cassandra.go) does not delete commits rows for a
// hard-deleted library either.
func cleanupPendingPublishedFileAttemptArtifacts(database *db.DB, repoID string, pending *pendingPublishedFile) error {
	if database == nil || pending == nil {
		return nil
	}
	fsID := strings.TrimSpace(pending.fsID)
	attemptID := strings.TrimSpace(pending.cleanupAttemptID)
	if fsID == "" || attemptID == "" {
		return nil
	}
	blockIDs := db.NormalizeBlockIDs(pending.internalBlockIDs)
	orgID := strings.TrimSpace(pending.cleanupOrgID)
	if len(blockIDs) > 0 && orgID == "" {
		return fmt.Errorf("cleanup metadata for fs_object %s is missing org_id", fsID)
	}
	if err := CleanupFailedPublishArtifacts(database, orgID, repoID, attemptID, "", []string{fsID}, blockIDs); err != nil {
		return fmt.Errorf("cleanup failed publish artifacts for fs_object %s attempt %s: %w", fsID, attemptID, err)
	}
	return nil
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

func repairPublishedBlockReferenceRepair(database *db.DB, repair publishedBlockReferenceRepair) error {
	if !shouldQueuePublishedBlockReferenceRepair(repair.FSID, repair.StagedBlockIDs) || strings.TrimSpace(repair.CommitID) == "" {
		return nil
	}
	if len(repair.StagedBlockIDs) == 0 {
		return fmt.Errorf("queued publish repair for fs_object %s has no staged block IDs", repair.FSID)
	}
	commitReachable, libraryGone, err := publishedBlockReferenceRepairCommitReachableFn(database, repair.OrgID, repair.RepoID, repair.CommitID)
	if err != nil {
		return err
	}
	switch {
	case commitReachable:
		pending, err := loadPublishedBlockReferenceRepairPendingFileFn(database, repair.RepoID, repair.FSID)
		if err != nil {
			return err
		}
		pending.internalBlockIDs = append([]string(nil), repair.StagedBlockIDs...)
		helper := NewFSHelper(database)
		if err := publishedBlockReferenceRepairPromoteFn(helper, repair.OrgID, repair.RepoID, repair.CommitID, pending); err != nil {
			return fmt.Errorf("promote published fs_object %s for commit %s: %w", repair.FSID, repair.CommitID, err)
		}
	case !libraryGone:
		// Unreachable from the current live HEAD, but publication authority is
		// not terminal: a slow writer or an ambiguous outcome may still be able
		// to win this library's HEAD CAS. Retain both durable liveness records;
		// timeouts and absence are not revocations.
		return nil
	default: // terminal publication authority
		if err := publishedBlockReferenceRepairCleanupFn(database, repair.OrgID, repair.RepoID, repair.CommitID, repair.FSID, repair.StagedBlockIDs); err != nil {
			return fmt.Errorf("cleanup unreachable published fs_object %s for commit %s: %w", repair.FSID, repair.CommitID, err)
		}
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

func RepairPublishedFSObjectBlockReferenceRepair(database *db.DB, orgID, repoID, commitID, fsID string, stagedBlockIDs []string) error {
	return repairPublishedBlockReferenceRepair(database, newPublishedBlockReferenceRepair(orgID, repoID, commitID, fsID, stagedBlockIDs))
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
			if err := repairPublishedBlockReferenceRepair(database, repair); err != nil {
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
			if err := runPendingPublishedFSObjectOwnerSweep(database); err != nil {
				log.Printf("[publish_repair] initial pending fs_object owner sweep failed: %v", err)
			}
			ticker := publishedBlockReferenceRepairTickerFn(publishedBlockReferenceRepairSweepInterval)
			defer ticker.Stop()
			for range ticker.C {
				if err := runPublishedBlockReferenceRepairSweep(database); err != nil {
					log.Printf("[publish_repair] periodic sweep failed: %v", err)
				}
				if err := runPendingPublishedFSObjectOwnerSweep(database); err != nil {
					log.Printf("[publish_repair] periodic pending fs_object owner sweep failed: %v", err)
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
