package v2

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

func TestSchedulePublishedBlockReferenceRepairDeduplicatesInFlightRepair(t *testing.T) {
	oldDelay := libraryHeadMutationRetryDelay
	oldMaxDelay := libraryHeadMutationRetryMaxDelay
	oldJitter := libraryHeadMutationRetryJitter
	oldRun := schedulePublishedBlockReferenceRepairRunFn
	oldSleep := schedulePublishedBlockReferenceRepairSleepFn
	t.Cleanup(func() {
		libraryHeadMutationRetryDelay = oldDelay
		libraryHeadMutationRetryMaxDelay = oldMaxDelay
		libraryHeadMutationRetryJitter = oldJitter
		schedulePublishedBlockReferenceRepairRunFn = oldRun
		schedulePublishedBlockReferenceRepairSleepFn = oldSleep
	})

	libraryHeadMutationRetryDelay = time.Millisecond
	libraryHeadMutationRetryMaxDelay = time.Millisecond
	libraryHeadMutationRetryJitter = 0
	var pending []func()
	var slept []time.Duration
	schedulePublishedBlockReferenceRepairRunFn = func(repair func()) {
		pending = append(pending, repair)
	}
	schedulePublishedBlockReferenceRepairSleepFn = func(delay time.Duration) {
		slept = append(slept, delay)
	}

	repairCalls := 0
	SchedulePublishedBlockReferenceRepair("repair-key", "test", func() error {
		repairCalls++
		return nil
	})
	SchedulePublishedBlockReferenceRepair("repair-key", "test", func() error {
		repairCalls += 100
		return nil
	})
	if len(pending) != 1 {
		t.Fatalf("scheduled repairs = %d, want 1", len(pending))
	}

	pending[0]()
	if repairCalls != 1 {
		t.Fatalf("repairCalls = %d, want 1", repairCalls)
	}
	if len(slept) != 1 || slept[0] != time.Millisecond {
		t.Fatalf("slept = %#v, want []time.Duration{time.Millisecond}", slept)
	}

	SchedulePublishedBlockReferenceRepair("repair-key", "test", func() error {
		repairCalls++
		return nil
	})
	if len(pending) != 2 {
		t.Fatalf("scheduled repairs after completion = %d, want 2", len(pending))
	}
	pending[1]()
}

func TestQueuePendingPublishedFileRepairs_RollsBackPartialInsertFailure(t *testing.T) {
	oldInsert := insertPublishedBlockReferenceRepairFn
	oldDelete := deletePublishedBlockReferenceRepairFn
	t.Cleanup(func() {
		insertPublishedBlockReferenceRepairFn = oldInsert
		deletePublishedBlockReferenceRepairFn = oldDelete
	})

	stageErr := errors.New("insert boom")
	cleanupErr := errors.New("cleanup boom")
	insertCalls := 0
	insertPublishedBlockReferenceRepairFn = func(database *db.DB, repair publishedBlockReferenceRepair) error {
		insertCalls++
		if insertCalls == 2 {
			return stageErr
		}
		return nil
	}
	deleteCalls := 0
	deletePublishedBlockReferenceRepairFn = func(database *db.DB, repair publishedBlockReferenceRepair) error {
		deleteCalls++
		if deleteCalls == 2 {
			return cleanupErr
		}
		return nil
	}

	pending := []*pendingPublishedFile{
		{fsID: "fs-1", externalBlockIDs: []string{"block-1"}, internalBlockIDs: []string{"staged-1"}},
		{fsID: "fs-2", externalBlockIDs: []string{"block-2"}, internalBlockIDs: []string{"staged-2"}},
	}
	err := queuePendingPublishedFileRepairs(nil, "org-1", "repo-1", "commit-1", pending)
	if !errors.Is(err, stageErr) {
		t.Fatalf("queuePendingPublishedFileRepairs() error = %v, want stage error %v", err, stageErr)
	}
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("queuePendingPublishedFileRepairs() error = %v, want cleanup error %v", err, cleanupErr)
	}
	if insertCalls != 2 {
		t.Fatalf("insertCalls = %d, want 2", insertCalls)
	}
	if deleteCalls != 2 {
		t.Fatalf("deleteCalls = %d, want 2", deleteCalls)
	}
}

func TestCleanupFailedPublishArtifacts_DeletesCommitAndDedupesAttemptRefs(t *testing.T) {
	oldDeleteCommit := cleanupFailedPublishDeleteCommitFn
	oldRemoveRefs := cleanupFailedPublishRemoveAttemptReferencesFn
	t.Cleanup(func() {
		cleanupFailedPublishDeleteCommitFn = oldDeleteCommit
		cleanupFailedPublishRemoveAttemptReferencesFn = oldRemoveRefs
	})

	commitDeletes := 0
	cleanupFailedPublishDeleteCommitFn = func(database *db.DB, repoID, commitID string) error {
		commitDeletes++
		if repoID != "repo-1" || commitID != "commit-losing" {
			t.Fatalf("delete commit args = %s/%s, want repo-1/commit-losing", repoID, commitID)
		}
		return nil
	}
	removeRefsCalls := 0
	cleanupFailedPublishRemoveAttemptReferencesFn = func(database *db.DB, orgID, attemptID string, blockIDs []string) error {
		removeRefsCalls++
		if orgID != "org-1" || attemptID != "attempt-1" {
			t.Fatalf("remove refs args = %s/%s, want org-1/attempt-1", orgID, attemptID)
		}
		if len(blockIDs) != 1 || blockIDs[0] != "queued-block-1" {
			t.Fatalf("remove refs blockIDs = %#v, want []string{\"queued-block-1\"}", blockIDs)
		}
		return nil
	}

	err := CleanupFailedPublishArtifacts(&db.DB{}, "org-1", "repo-1", "attempt-1", "commit-losing", []string{"fs-live", "fs-zombie"}, []string{"queued-block-1", "queued-block-1"})
	if err != nil {
		t.Fatalf("CleanupFailedPublishArtifacts() error = %v, want nil", err)
	}
	if commitDeletes != 1 {
		t.Fatalf("commitDeletes = %d, want 1", commitDeletes)
	}
	if removeRefsCalls != 1 {
		t.Fatalf("removeRefsCalls = %d, want 1", removeRefsCalls)
	}
}

func TestCleanupFailedPublishArtifacts_ReturnsCommitDeleteErrorAfterRemovingRefs(t *testing.T) {
	oldDeleteCommit := cleanupFailedPublishDeleteCommitFn
	oldRemoveRefs := cleanupFailedPublishRemoveAttemptReferencesFn
	t.Cleanup(func() {
		cleanupFailedPublishDeleteCommitFn = oldDeleteCommit
		cleanupFailedPublishRemoveAttemptReferencesFn = oldRemoveRefs
	})

	commitErr := errors.New("commit delete failed")
	cleanupFailedPublishDeleteCommitFn = func(database *db.DB, repoID, commitID string) error {
		return commitErr
	}
	removeRefsCalls := 0
	cleanupFailedPublishRemoveAttemptReferencesFn = func(database *db.DB, orgID, attemptID string, blockIDs []string) error {
		removeRefsCalls++
		return nil
	}

	err := CleanupFailedPublishArtifacts(&db.DB{}, "org-1", "repo-1", "attempt-1", "commit-losing", []string{"fs-zombie"}, []string{"queued-block-1"})
	if !errors.Is(err, commitErr) {
		t.Fatalf("CleanupFailedPublishArtifacts() error = %v, want commitErr %v", err, commitErr)
	}
	if removeRefsCalls != 1 {
		t.Fatalf("removeRefsCalls = %d, want 1", removeRefsCalls)
	}
}

func TestCleanupFailedPublishAttempt_PreservesPendingOwnersWhenArtifactCleanupFails(t *testing.T) {
	oldDeleteCommit := cleanupFailedPublishDeleteCommitFn
	oldRelease := releasePendingPublishedFileOwnersFn
	t.Cleanup(func() {
		cleanupFailedPublishDeleteCommitFn = oldDeleteCommit
		releasePendingPublishedFileOwnersFn = oldRelease
	})

	commitErr := errors.New("commit delete failed")
	cleanupFailedPublishDeleteCommitFn = func(database *db.DB, repoID, commitID string) error {
		return commitErr
	}
	releaseCalls := 0
	releasePendingPublishedFileOwnersFn = func(database *db.DB, repoID string, pendingFiles []*pendingPublishedFile) error {
		releaseCalls++
		return nil
	}

	err := CleanupFailedPublishAttempt(&db.DB{}, "org-1", "repo-1", "commit-1", "commit-1", []*pendingPublishedFile{{
		fsID:             "fs-1",
		cleanupOwnerID:   "owner-1",
		cleanupCreatedAt: time.Now().UTC(),
	}})
	if !errors.Is(err, commitErr) {
		t.Fatalf("CleanupFailedPublishAttempt() error = %v, want commitErr %v", err, commitErr)
	}
	if releaseCalls != 0 {
		t.Fatalf("releaseCalls = %d, want 0", releaseCalls)
	}
}

func TestClearPendingPublishedFileOwners_DeletesOwnerWithoutReachabilityChecks(t *testing.T) {
	oldDeletePendingOwner := cleanupFailedPublishDeletePendingOwnerFn
	oldOwnerExists := cleanupFailedPublishPendingOwnerExistsFn
	oldReachable := cleanupFailedPublishFSObjectReachableFn
	t.Cleanup(func() {
		cleanupFailedPublishDeletePendingOwnerFn = oldDeletePendingOwner
		cleanupFailedPublishPendingOwnerExistsFn = oldOwnerExists
		cleanupFailedPublishFSObjectReachableFn = oldReachable
	})

	deleteCalls := 0
	cleanupFailedPublishDeletePendingOwnerFn = func(database *db.DB, repoID, fsID, ownerID string, createdAt time.Time) error {
		deleteCalls++
		return nil
	}
	cleanupFailedPublishPendingOwnerExistsFn = func(database *db.DB, repoID, fsID string) (bool, error) {
		t.Fatal("clearPendingPublishedFileOwners should not check remaining owners")
		return false, nil
	}
	cleanupFailedPublishFSObjectReachableFn = func(database *db.DB, repoID, fsID string) (bool, error) {
		t.Fatal("clearPendingPublishedFileOwners should not check reachability")
		return false, nil
	}

	err := clearPendingPublishedFileOwners(&db.DB{}, "repo-1", []*pendingPublishedFile{{
		fsID:             "fs-1",
		cleanupOwnerID:   "owner-1",
		cleanupCreatedAt: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatalf("clearPendingPublishedFileOwners() error = %v, want nil", err)
	}
	if deleteCalls != 1 {
		t.Fatalf("deleteCalls = %d, want 1", deleteCalls)
	}
}

func TestCleanupFailedPublishAttempt_ReleasesOwnerWithoutDeletingSharedFSObject(t *testing.T) {
	oldDeletePendingOwner := cleanupFailedPublishDeletePendingOwnerFn
	oldOwnerExists := cleanupFailedPublishPendingOwnerExistsFn
	oldReachable := cleanupFailedPublishFSObjectReachableFn
	oldDeleteFSObject := cleanupFailedPublishDeleteFSObjectFn
	t.Cleanup(func() {
		cleanupFailedPublishDeletePendingOwnerFn = oldDeletePendingOwner
		cleanupFailedPublishPendingOwnerExistsFn = oldOwnerExists
		cleanupFailedPublishFSObjectReachableFn = oldReachable
		cleanupFailedPublishDeleteFSObjectFn = oldDeleteFSObject
	})

	deletedOwners := 0
	cleanupFailedPublishDeletePendingOwnerFn = func(database *db.DB, repoID, fsID, ownerID string, createdAt time.Time) error {
		deletedOwners++
		if repoID != "repo-1" || fsID != "fs-1" || ownerID != "owner-1" {
			t.Fatalf("delete pending owner args = %s/%s/%s, want repo-1/fs-1/owner-1", repoID, fsID, ownerID)
		}
		return nil
	}
	cleanupFailedPublishPendingOwnerExistsFn = func(database *db.DB, repoID, fsID string) (bool, error) {
		t.Fatal("release should not check owner existence before deciding whether to delete a shared fs_object")
		return false, nil
	}
	cleanupFailedPublishFSObjectReachableFn = func(database *db.DB, repoID, fsID string) (bool, error) {
		t.Fatal("release should not check reachability before deciding whether to delete a shared fs_object")
		return false, nil
	}
	cleanupFailedPublishDeleteFSObjectFn = func(database *db.DB, repoID, fsID string) error {
		t.Fatal("release should not delete content-addressed fs_object rows")
		return nil
	}

	err := CleanupFailedPublishAttempt(&db.DB{}, "", "repo-1", "", "", []*pendingPublishedFile{{
		fsID:             "fs-1",
		cleanupOwnerID:   "owner-1",
		cleanupCreatedAt: time.Date(2026, time.June, 1, 10, 0, 0, 0, time.UTC),
	}})
	if err != nil {
		t.Fatalf("CleanupFailedPublishAttempt() error = %v, want nil", err)
	}
	if deletedOwners != 1 {
		t.Fatalf("deletedOwners = %d, want 1", deletedOwners)
	}
}

func TestFailedPublishFSObjectReachableFromRoot_IgnoresMissingUnrelatedNodes(t *testing.T) {
	oldLoad := failedPublishReachabilityLoadFSObjectFn
	t.Cleanup(func() {
		failedPublishReachabilityLoadFSObjectFn = oldLoad
	})

	failedPublishReachabilityLoadFSObjectFn = func(database *db.DB, repoID, fsID string) (string, string, error) {
		if repoID != "repo-1" {
			t.Fatalf("repoID = %q, want repo-1", repoID)
		}
		switch fsID {
		case "root-fs":
			return "dir", `[{"name":"target","id":"target-fs"},{"name":"missing","id":"missing-fs"}]`, nil
		case "missing-fs":
			return "", "", gocql.ErrNotFound
		default:
			t.Fatalf("unexpected fsID lookup %q", fsID)
			return "", "", nil
		}
	}

	reachable, err := failedPublishFSObjectReachableFromRoot(&db.DB{}, "repo-1", "target-fs", "root-fs", map[string]bool{})
	if err != nil {
		t.Fatalf("failedPublishFSObjectReachableFromRoot() error = %v, want nil", err)
	}
	if !reachable {
		t.Fatal("failedPublishFSObjectReachableFromRoot() = false, want true when target remains reachable past a missing sibling")
	}
}

func TestRunPendingPublishedFSObjectOwnerSweep_ReleasesOnlyStaleOwners(t *testing.T) {
	oldNow := pendingPublishedFSObjectOwnerNowFn
	oldList := listPendingPublishedFSObjectOwnersByDayFn
	oldLoad := loadPendingPublishedFSObjectOwnerFn
	oldCleanup := cleanupPendingPublishedFileOwnerAttemptFn
	t.Cleanup(func() {
		pendingPublishedFSObjectOwnerNowFn = oldNow
		listPendingPublishedFSObjectOwnersByDayFn = oldList
		loadPendingPublishedFSObjectOwnerFn = oldLoad
		cleanupPendingPublishedFileOwnerAttemptFn = oldCleanup
	})

	now := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	pendingPublishedFSObjectOwnerNowFn = func() time.Time { return now }
	listPendingPublishedFSObjectOwnersByDayFn = func(database *db.DB, day time.Time, bucket int) ([]db.PendingPublishedFSObjectOwner, error) {
		if !day.Equal(db.GCProjectionUTCDate(now)) || bucket != 0 {
			return nil, nil
		}
		return []db.PendingPublishedFSObjectOwner{
			{RepoID: "repo-1", FSID: "fs-stale", OwnerID: "owner-stale", CreatedAt: now.Add(-pendingPublishedFSObjectOwnerStaleAfter - time.Minute), OrgID: "org-1", AttemptID: "commit-stale", BlockIDs: []string{"queued-block-1"}},
			{RepoID: "repo-1", FSID: "fs-fresh", OwnerID: "owner-fresh", CreatedAt: now.Add(-time.Hour)},
		}, nil
	}
	loadPendingPublishedFSObjectOwnerFn = func(database *db.DB, repoID, fsID, ownerID string) (db.PendingPublishedFSObjectOwner, error) {
		return db.PendingPublishedFSObjectOwner{}, nil
	}
	var released []string
	cleanupPendingPublishedFileOwnerAttemptFn = func(database *db.DB, repoID string, pending *pendingPublishedFile) error {
		if pending.cleanupOrgID != "org-1" || pending.cleanupAttemptID != "commit-stale" {
			t.Fatalf("pending cleanup metadata = %s/%s, want org-1/commit-stale", pending.cleanupOrgID, pending.cleanupAttemptID)
		}
		if len(pending.internalBlockIDs) != 1 || pending.internalBlockIDs[0] != "queued-block-1" {
			t.Fatalf("pending.internalBlockIDs = %#v, want []string{\"queued-block-1\"}", pending.internalBlockIDs)
		}
		released = append(released, repoID+":"+pending.fsID+":"+pending.cleanupOwnerID)
		return nil
	}

	err := runPendingPublishedFSObjectOwnerSweep(&db.DB{})
	if err != nil {
		t.Fatalf("runPendingPublishedFSObjectOwnerSweep() error = %v, want nil", err)
	}
	if len(released) != 1 || released[0] != "repo-1:fs-stale:owner-stale" {
		t.Fatalf("released = %#v, want []string{\"repo-1:fs-stale:owner-stale\"}", released)
	}
}

func TestRunPendingPublishedFSObjectOwnerSweep_HydratesMissingMetadataFromPrimaryRow(t *testing.T) {
	oldNow := pendingPublishedFSObjectOwnerNowFn
	oldList := listPendingPublishedFSObjectOwnersByDayFn
	oldLoad := loadPendingPublishedFSObjectOwnerFn
	oldCleanup := cleanupPendingPublishedFileOwnerAttemptFn
	t.Cleanup(func() {
		pendingPublishedFSObjectOwnerNowFn = oldNow
		listPendingPublishedFSObjectOwnersByDayFn = oldList
		loadPendingPublishedFSObjectOwnerFn = oldLoad
		cleanupPendingPublishedFileOwnerAttemptFn = oldCleanup
	})

	now := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	pendingPublishedFSObjectOwnerNowFn = func() time.Time { return now }
	listPendingPublishedFSObjectOwnersByDayFn = func(database *db.DB, day time.Time, bucket int) ([]db.PendingPublishedFSObjectOwner, error) {
		if !day.Equal(db.GCProjectionUTCDate(now)) || bucket != 0 {
			return nil, nil
		}
		return []db.PendingPublishedFSObjectOwner{{
			RepoID:    "repo-1",
			FSID:      "fs-stale",
			OwnerID:   "owner-stale",
			CreatedAt: now.Add(-pendingPublishedFSObjectOwnerStaleAfter - time.Minute),
		}}, nil
	}
	hydrated := 0
	loadPendingPublishedFSObjectOwnerFn = func(database *db.DB, repoID, fsID, ownerID string) (db.PendingPublishedFSObjectOwner, error) {
		hydrated++
		if repoID != "repo-1" || fsID != "fs-stale" || ownerID != "owner-stale" {
			t.Fatalf("hydrate args = %s/%s/%s, want repo-1/fs-stale/owner-stale", repoID, fsID, ownerID)
		}
		return db.PendingPublishedFSObjectOwner{
			RepoID:    repoID,
			FSID:      fsID,
			OwnerID:   ownerID,
			CreatedAt: now.Add(-pendingPublishedFSObjectOwnerStaleAfter - time.Minute),
			OrgID:     "org-1",
			AttemptID: "commit-stale",
			BlockIDs:  []string{"queued-block-1", "queued-block-2"},
		}, nil
	}
	cleanupPendingPublishedFileOwnerAttemptFn = func(database *db.DB, repoID string, pending *pendingPublishedFile) error {
		if repoID != "repo-1" {
			t.Fatalf("cleanup repoID = %s, want repo-1", repoID)
		}
		if pending.cleanupOrgID != "org-1" || pending.cleanupAttemptID != "commit-stale" {
			t.Fatalf("pending cleanup metadata = %s/%s, want org-1/commit-stale", pending.cleanupOrgID, pending.cleanupAttemptID)
		}
		if !reflect.DeepEqual(pending.internalBlockIDs, []string{"queued-block-1", "queued-block-2"}) {
			t.Fatalf("pending.internalBlockIDs = %#v, want hydrated block ids", pending.internalBlockIDs)
		}
		return nil
	}

	if err := runPendingPublishedFSObjectOwnerSweep(&db.DB{}); err != nil {
		t.Fatalf("runPendingPublishedFSObjectOwnerSweep() error = %v, want nil", err)
	}
	if hydrated != 1 {
		t.Fatalf("hydrated = %d, want 1", hydrated)
	}
}

func TestRunPendingPublishedFSObjectOwnerSweep_DeletesDanglingProjectionWhenPrimaryRowMissing(t *testing.T) {
	oldNow := pendingPublishedFSObjectOwnerNowFn
	oldList := listPendingPublishedFSObjectOwnersByDayFn
	oldLoad := loadPendingPublishedFSObjectOwnerFn
	oldDeletePendingOwner := cleanupFailedPublishDeletePendingOwnerFn
	oldCleanup := cleanupPendingPublishedFileOwnerAttemptFn
	t.Cleanup(func() {
		pendingPublishedFSObjectOwnerNowFn = oldNow
		listPendingPublishedFSObjectOwnersByDayFn = oldList
		loadPendingPublishedFSObjectOwnerFn = oldLoad
		cleanupFailedPublishDeletePendingOwnerFn = oldDeletePendingOwner
		cleanupPendingPublishedFileOwnerAttemptFn = oldCleanup
	})

	now := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	pendingPublishedFSObjectOwnerNowFn = func() time.Time { return now }
	listPendingPublishedFSObjectOwnersByDayFn = func(database *db.DB, day time.Time, bucket int) ([]db.PendingPublishedFSObjectOwner, error) {
		if !day.Equal(db.GCProjectionUTCDate(now)) || bucket != 0 {
			return nil, nil
		}
		return []db.PendingPublishedFSObjectOwner{{
			RepoID:    "repo-1",
			FSID:      "fs-stale",
			OwnerID:   "owner-stale",
			CreatedAt: now.Add(-pendingPublishedFSObjectOwnerStaleAfter - time.Minute),
		}}, nil
	}
	loadPendingPublishedFSObjectOwnerFn = func(database *db.DB, repoID, fsID, ownerID string) (db.PendingPublishedFSObjectOwner, error) {
		return db.PendingPublishedFSObjectOwner{}, gocql.ErrNotFound
	}
	deleted := 0
	cleanupFailedPublishDeletePendingOwnerFn = func(database *db.DB, repoID, fsID, ownerID string, createdAt time.Time) error {
		deleted++
		if repoID != "repo-1" || fsID != "fs-stale" || ownerID != "owner-stale" {
			t.Fatalf("delete args = %s/%s/%s, want repo-1/fs-stale/owner-stale", repoID, fsID, ownerID)
		}
		if !createdAt.Equal(now.Add(-pendingPublishedFSObjectOwnerStaleAfter - time.Minute)) {
			t.Fatalf("delete createdAt = %v, want projection timestamp", createdAt)
		}
		return nil
	}
	cleanupPendingPublishedFileOwnerAttemptFn = func(database *db.DB, repoID string, pending *pendingPublishedFile) error {
		t.Fatal("cleanupPendingPublishedFileOwnerAttemptFn must not run for dangling projections")
		return nil
	}

	if err := runPendingPublishedFSObjectOwnerSweep(&db.DB{}); err != nil {
		t.Fatalf("runPendingPublishedFSObjectOwnerSweep() error = %v, want nil", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
}

func TestCleanupPendingPublishedFileOwnerAttempt_PromotesReachableCommitBeforeClearingOwner(t *testing.T) {
	oldReachable := cleanupPendingPublishedFileAttemptCommitReachableFn
	oldLoad := loadPublishedBlockReferenceRepairPendingFileFn
	oldPromote := publishedBlockReferenceRepairPromoteFn
	oldClear := clearPendingPublishedFileOwnerFn
	oldDeleteCommit := cleanupFailedPublishDeleteCommitFn
	oldRemoveAttemptRefs := cleanupFailedPublishRemoveAttemptReferencesFn
	t.Cleanup(func() {
		cleanupPendingPublishedFileAttemptCommitReachableFn = oldReachable
		loadPublishedBlockReferenceRepairPendingFileFn = oldLoad
		publishedBlockReferenceRepairPromoteFn = oldPromote
		clearPendingPublishedFileOwnerFn = oldClear
		cleanupFailedPublishDeleteCommitFn = oldDeleteCommit
		cleanupFailedPublishRemoveAttemptReferencesFn = oldRemoveAttemptRefs
	})

	reachabilityChecks := 0
	cleanupPendingPublishedFileAttemptCommitReachableFn = func(database *db.DB, orgID, repoID, commitID string) (publishedBlockReferenceRepairCommitOutcome, error) {
		reachabilityChecks++
		if repoID != "repo-1" || commitID != "commit-1" {
			t.Fatalf("reachability args = %s/%s, want repo-1/commit-1", repoID, commitID)
		}
		return publishedBlockReferenceRepairCommitReachable, nil
	}
	loaded := 0
	loadPublishedBlockReferenceRepairPendingFileFn = func(database *db.DB, repoID, fsID string) (*pendingPublishedFile, error) {
		loaded++
		if repoID != "repo-1" || fsID != "fs-1" {
			t.Fatalf("load args = %s/%s, want repo-1/fs-1", repoID, fsID)
		}
		return &pendingPublishedFile{fsID: fsID, externalBlockIDs: []string{"block-ext-1"}}, nil
	}
	promoted := 0
	publishedBlockReferenceRepairPromoteFn = func(helper *FSHelper, orgID, repoID, commitID string, pending *pendingPublishedFile) error {
		promoted++
		if helper == nil {
			t.Fatal("promote helper must be initialized")
		}
		if orgID != "org-1" || repoID != "repo-1" || commitID != "commit-1" {
			t.Fatalf("promote args = %s/%s/%s, want org-1/repo-1/commit-1", orgID, repoID, commitID)
		}
		if pending == nil || pending.fsID != "fs-1" {
			t.Fatalf("promote pending fsID = %#v, want fs-1", pending)
		}
		if !reflect.DeepEqual(pending.externalBlockIDs, []string{"block-ext-1"}) {
			t.Fatalf("promote externalBlockIDs = %#v, want []string{\"block-ext-1\"}", pending.externalBlockIDs)
		}
		if !reflect.DeepEqual(pending.internalBlockIDs, []string{"block-int-1"}) {
			t.Fatalf("promote internalBlockIDs = %#v, want []string{\"block-int-1\"}", pending.internalBlockIDs)
		}
		return nil
	}
	clearedOwners := 0
	clearPendingPublishedFileOwnerFn = func(database *db.DB, repoID string, pending *pendingPublishedFile) error {
		clearedOwners++
		if repoID != "repo-1" || pending == nil || pending.fsID != "fs-1" || pending.cleanupOwnerID != "owner-1" {
			t.Fatalf("clear owner args = %s/%#v, want repo-1 owner fs-1/owner-1", repoID, pending)
		}
		return nil
	}
	cleanupFailedPublishDeleteCommitFn = func(database *db.DB, repoID, commitID string) error {
		t.Fatal("published commit cleanup must not run for reachable commits")
		return nil
	}
	cleanupFailedPublishRemoveAttemptReferencesFn = func(database *db.DB, orgID, attemptID string, blockIDs []string) error {
		t.Fatal("publish-attempt ref cleanup must not run for reachable commits")
		return nil
	}

	err := cleanupPendingPublishedFileOwnerAttempt(&db.DB{}, "repo-1", &pendingPublishedFile{
		fsID:             "fs-1",
		cleanupOwnerID:   "owner-1",
		cleanupCreatedAt: time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC),
		cleanupOrgID:     "org-1",
		cleanupAttemptID: "commit-1",
		internalBlockIDs: []string{"block-int-1"},
	})
	if err != nil {
		t.Fatalf("cleanupPendingPublishedFileOwnerAttempt() error = %v, want nil", err)
	}
	if reachabilityChecks != 1 {
		t.Fatalf("reachabilityChecks = %d, want 1", reachabilityChecks)
	}
	if loaded != 1 {
		t.Fatalf("loaded = %d, want 1", loaded)
	}
	if promoted != 1 {
		t.Fatalf("promoted = %d, want 1", promoted)
	}
	if clearedOwners != 1 {
		t.Fatalf("clearedOwners = %d, want 1", clearedOwners)
	}
}

func TestCleanupPendingPublishedFileOwnerAttempt_FailsClosedWithoutAttemptMetadata(t *testing.T) {
	oldReachable := cleanupPendingPublishedFileAttemptCommitReachableFn
	oldDeletePendingOwner := cleanupFailedPublishDeletePendingOwnerFn
	oldDeleteCommit := cleanupFailedPublishDeleteCommitFn
	t.Cleanup(func() {
		cleanupPendingPublishedFileAttemptCommitReachableFn = oldReachable
		cleanupFailedPublishDeletePendingOwnerFn = oldDeletePendingOwner
		cleanupFailedPublishDeleteCommitFn = oldDeleteCommit
	})

	reachabilityChecks := 0
	cleanupPendingPublishedFileAttemptCommitReachableFn = func(database *db.DB, orgID, repoID, commitID string) (publishedBlockReferenceRepairCommitOutcome, error) {
		reachabilityChecks++
		return publishedBlockReferenceRepairCommitUnknown, nil
	}
	clearedOwners := 0
	cleanupFailedPublishDeletePendingOwnerFn = func(database *db.DB, repoID, fsID, ownerID string, createdAt time.Time) error {
		clearedOwners++
		return nil
	}
	deleteCommitCalls := 0
	cleanupFailedPublishDeleteCommitFn = func(database *db.DB, repoID, commitID string) error {
		deleteCommitCalls++
		return nil
	}

	err := cleanupPendingPublishedFileOwnerAttempt(&db.DB{}, "repo-1", &pendingPublishedFile{
		fsID:             "fs-1",
		cleanupOwnerID:   "owner-1",
		cleanupCreatedAt: time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC),
		cleanupAttemptID: "   ",
	})
	if err == nil || !strings.Contains(err.Error(), "missing cleanup attempt metadata") {
		t.Fatalf("cleanupPendingPublishedFileOwnerAttempt() error = %v, want missing cleanup attempt metadata", err)
	}
	if reachabilityChecks != 0 {
		t.Fatalf("reachabilityChecks = %d, want 0", reachabilityChecks)
	}
	if clearedOwners != 0 {
		t.Fatalf("clearedOwners = %d, want 0", clearedOwners)
	}
	if deleteCommitCalls != 0 {
		t.Fatalf("deleteCommitCalls = %d, want 0", deleteCommitCalls)
	}
}

func TestRepairPublishedFSObjectBlockReferenceRepair_PromotesReachableCommit(t *testing.T) {
	oldReachable := publishedBlockReferenceRepairCommitReachableFn
	oldLoad := loadPublishedBlockReferenceRepairPendingFileFn
	oldPromote := publishedBlockReferenceRepairPromoteFn
	oldDelete := deletePublishedBlockReferenceRepairFn
	t.Cleanup(func() {
		publishedBlockReferenceRepairCommitReachableFn = oldReachable
		loadPublishedBlockReferenceRepairPendingFileFn = oldLoad
		publishedBlockReferenceRepairPromoteFn = oldPromote
		deletePublishedBlockReferenceRepairFn = oldDelete
	})

	publishedBlockReferenceRepairCommitReachableFn = func(database *db.DB, orgID, repoID, commitID string) (publishedBlockReferenceRepairCommitOutcome, error) {
		return publishedBlockReferenceRepairCommitReachable, nil
	}
	loadPublishedBlockReferenceRepairPendingFileFn = func(database *db.DB, repoID, fsID string) (*pendingPublishedFile, error) {
		return &pendingPublishedFile{fsID: fsID, externalBlockIDs: []string{"fs-block-1"}}, nil
	}
	promoteCalls := 0
	events := make([]string, 0, 2)
	publishedBlockReferenceRepairPromoteFn = func(helper *FSHelper, orgID, repoID, commitID string, pending *pendingPublishedFile) error {
		promoteCalls++
		events = append(events, "promote")
		if orgID != "org-1" || repoID != "repo-1" || commitID != "commit-1" {
			t.Fatalf("promote args = %s/%s/%s, want org-1/repo-1/commit-1", orgID, repoID, commitID)
		}
		if len(pending.internalBlockIDs) != 1 || pending.internalBlockIDs[0] != "queued-block-1" {
			t.Fatalf("pending.internalBlockIDs = %#v, want []string{\"queued-block-1\"}", pending.internalBlockIDs)
		}
		if len(pending.externalBlockIDs) != 1 || pending.externalBlockIDs[0] != "fs-block-1" {
			t.Fatalf("pending.externalBlockIDs = %#v, want []string{\"fs-block-1\"}", pending.externalBlockIDs)
		}
		return nil
	}
	deleteCalls := 0
	deletePublishedBlockReferenceRepairFn = func(database *db.DB, repair publishedBlockReferenceRepair) error {
		deleteCalls++
		events = append(events, "delete")
		if repair.RepoID != "repo-1" || repair.CommitID != "commit-1" || repair.FSID != "fs-1" {
			t.Fatalf("delete repair = %#v, want repo-1/commit-1/fs-1", repair)
		}
		return nil
	}

	err := RepairPublishedFSObjectBlockReferenceRepair(nil, "org-1", "repo-1", "commit-1", "fs-1", []string{"queued-block-1"})
	if err != nil {
		t.Fatalf("RepairPublishedFSObjectBlockReferenceRepair() error = %v, want nil", err)
	}
	if promoteCalls != 1 {
		t.Fatalf("promoteCalls = %d, want 1", promoteCalls)
	}
	if deleteCalls != 1 {
		t.Fatalf("deleteCalls = %d, want 1", deleteCalls)
	}
	if !reflect.DeepEqual(events, []string{"promote", "delete"}) {
		t.Fatalf("repair settlement order = %#v, want promote before delete", events)
	}
}

func TestRepairPublishedFSObjectBlockReferenceRepair_RetainsUnknownOutcomeAfterLeaseExpiry(t *testing.T) {
	oldReachable := publishedBlockReferenceRepairCommitReachableFn
	oldHead := publishedBlockReferenceRepairHeadCommitFn
	oldParent := publishedBlockReferenceRepairCommitParentFn
	oldLoad := loadPublishedBlockReferenceRepairPendingFileFn
	oldPromote := publishedBlockReferenceRepairPromoteFn
	oldDelete := deletePublishedBlockReferenceRepairFn
	oldNow := publishedBlockReferenceRepairNowFn
	t.Cleanup(func() {
		publishedBlockReferenceRepairCommitReachableFn = oldReachable
		publishedBlockReferenceRepairHeadCommitFn = oldHead
		publishedBlockReferenceRepairCommitParentFn = oldParent
		loadPublishedBlockReferenceRepairPendingFileFn = oldLoad
		publishedBlockReferenceRepairPromoteFn = oldPromote
		deletePublishedBlockReferenceRepairFn = oldDelete
		publishedBlockReferenceRepairNowFn = oldNow
	})

	now := time.Date(2026, time.May, 29, 12, 0, 0, 0, time.UTC)
	publishedBlockReferenceRepairCommitReachableFn = func(database *db.DB, orgID, repoID, commitID string) (publishedBlockReferenceRepairCommitOutcome, error) {
		return publishedBlockReferenceRepairCommitUnknown, nil
	}
	publishedBlockReferenceRepairNowFn = func() time.Time {
		return now
	}
	publishedBlockReferenceRepairHeadCommitFn = func(database *db.DB, orgID, repoID string) (string, error) {
		t.Fatal("head lookup should not be repeated after the outcome hook returns UNKNOWN")
		return "", nil
	}
	publishedBlockReferenceRepairCommitParentFn = func(database *db.DB, repoID, commitID string) (string, error) {
		t.Fatal("parent lookup should not be repeated after the outcome hook returns UNKNOWN")
		return "", nil
	}
	loadPublishedBlockReferenceRepairPendingFileFn = func(database *db.DB, repoID, fsID string) (*pendingPublishedFile, error) {
		t.Fatal("fs_object lookup should not run for unknown publication")
		return nil, nil
	}
	publishedBlockReferenceRepairPromoteFn = func(helper *FSHelper, orgID, repoID, commitID string, pending *pendingPublishedFile) error {
		t.Fatal("promote should not run for unknown publication")
		return nil
	}
	deletePublishedBlockReferenceRepairFn = func(database *db.DB, repair publishedBlockReferenceRepair) error {
		t.Fatal("repair row should not be deleted for unknown publication")
		return nil
	}

	repair := publishedBlockReferenceRepair{
		Bucket:         publishedBlockReferenceRepairBucket("org-1", "repo-1", "commit-1", "fs-1"),
		OrgID:          "org-1",
		RepoID:         "repo-1",
		CommitID:       "commit-1",
		FSID:           "fs-1",
		StagedBlockIDs: []string{"queued-block-1"},
		CreatedAt:      now.Add(-10 * time.Minute),
		LeaseExpiresAt: now.Add(-time.Minute),
	}
	err := repairPublishedBlockReferenceRepair(nil, repair)
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("repairPublishedBlockReferenceRepair() error = %v, want unknown-publication retention error", err)
	}
}

func TestPublishedBlockReferenceRepairRetryDelayIsCappedAndAgeBased(t *testing.T) {
	now := time.Date(2026, time.May, 29, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		createdAt time.Time
		want      time.Duration
	}{
		{name: "young row uses base", createdAt: now.Add(-time.Minute), want: publishedBlockReferenceRepairRetryBase},
		{name: "older row backs off with age", createdAt: now.Add(-time.Hour), want: time.Hour},
		{name: "very old row is capped", createdAt: now.Add(-48 * time.Hour), want: publishedBlockReferenceRepairRetryMax},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := publishedBlockReferenceRepairRetryDelay(now, tt.createdAt); got != tt.want {
				t.Fatalf("retry delay = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRunPublishedBlockReferenceRepairSweepUsesAdvisoryRetrySchedule(t *testing.T) {
	oldNow := publishedBlockReferenceRepairNowFn
	oldList := listPublishedBlockReferenceRepairsForBucketFn
	oldReachable := publishedBlockReferenceRepairCommitReachableFn
	oldSchedule := schedulePublishedBlockReferenceRepairRetryFn
	t.Cleanup(func() {
		publishedBlockReferenceRepairNowFn = oldNow
		listPublishedBlockReferenceRepairsForBucketFn = oldList
		publishedBlockReferenceRepairCommitReachableFn = oldReachable
		schedulePublishedBlockReferenceRepairRetryFn = oldSchedule
	})

	now := time.Date(2026, time.May, 29, 12, 0, 0, 0, time.UTC)
	publishedBlockReferenceRepairNowFn = func() time.Time { return now }
	repair := publishedBlockReferenceRepair{
		Bucket:         0,
		OrgID:          "org-1",
		RepoID:         "repo-1",
		CommitID:       "commit-1",
		FSID:           "fs-1",
		StagedBlockIDs: []string{"block-1"},
		CreatedAt:      now.Add(-time.Hour),
		LeaseExpiresAt: now.Add(time.Hour),
	}
	listPublishedBlockReferenceRepairsForBucketFn = func(database *db.DB, bucket int) ([]publishedBlockReferenceRepair, error) {
		if bucket == 0 {
			return []publishedBlockReferenceRepair{repair}, nil
		}
		return nil, nil
	}
	reachableCalls := 0
	publishedBlockReferenceRepairCommitReachableFn = func(database *db.DB, orgID, repoID, commitID string) (publishedBlockReferenceRepairCommitOutcome, error) {
		reachableCalls++
		return publishedBlockReferenceRepairCommitUnknown, nil
	}
	scheduled := 0
	var nextRetryAt time.Time
	schedulePublishedBlockReferenceRepairRetryFn = func(database *db.DB, got publishedBlockReferenceRepair, retryAt time.Time) error {
		scheduled++
		nextRetryAt = retryAt
		if got.RepoID != repair.RepoID || got.CommitID != repair.CommitID || got.FSID != repair.FSID {
			t.Fatalf("scheduled repair = %#v, want %#v", got, repair)
		}
		return nil
	}

	if err := runPublishedBlockReferenceRepairSweep(&db.DB{}); err != nil {
		t.Fatalf("future advisory lease made sweep fail: %v", err)
	}
	if reachableCalls != 0 || scheduled != 0 {
		t.Fatalf("future advisory retry was used: reachableCalls=%d scheduled=%d", reachableCalls, scheduled)
	}

	repair.LeaseExpiresAt = now.Add(-time.Second)
	if err := runPublishedBlockReferenceRepairSweep(&db.DB{}); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("due unknown repair error = %v, want unknown retention error", err)
	}
	if reachableCalls != 1 || scheduled != 1 {
		t.Fatalf("due unknown repair calls = reachable %d scheduled %d, want 1/1", reachableCalls, scheduled)
	}
	if !nextRetryAt.After(now) {
		t.Fatalf("nextRetryAt = %s, want future advisory retry", nextRetryAt)
	}
}

func TestShouldRunPendingPublishedFSObjectOwnerSweepUsesFifteenMinuteCadence(t *testing.T) {
	now := time.Date(2026, time.May, 29, 12, 0, 0, 0, time.UTC)
	if !shouldRunPendingPublishedFSObjectOwnerSweep(time.Time{}, now) {
		t.Fatal("initial owner sweep must run")
	}
	if shouldRunPendingPublishedFSObjectOwnerSweep(now, now.Add(14*time.Minute+59*time.Second)) {
		t.Fatal("owner sweep ran before its advisory cadence")
	}
	if !shouldRunPendingPublishedFSObjectOwnerSweep(now, now.Add(pendingPublishedFSObjectOwnerSweepInterval)) {
		t.Fatal("owner sweep did not run at its advisory cadence")
	}
}

func TestClassifyPublishedBlockReferenceRepairCommitOutcome(t *testing.T) {
	tests := []struct {
		name         string
		commitID     string
		headCommitID string
		parents      map[string]string
		want         publishedBlockReferenceRepairCommitOutcome
		wantErr      bool
	}{
		{
			name:         "head commit is reachable",
			commitID:     "c3",
			headCommitID: "c3",
			parents:      map[string]string{"c3": "c2", "c2": "c1", "c1": ""},
			want:         publishedBlockReferenceRepairCommitReachable,
		},
		{
			name:         "ancestor is reachable",
			commitID:     "c1",
			headCommitID: "c3",
			parents:      map[string]string{"c3": "c2", "c2": "c1", "c1": ""},
			want:         publishedBlockReferenceRepairCommitReachable,
		},
		{
			name:         "head stayed at expected parent remains unknown",
			commitID:     "c2",
			headCommitID: "c1",
			parents:      map[string]string{"c2": "c1", "c1": ""},
			want:         publishedBlockReferenceRepairCommitUnknown,
		},
		{
			name:         "concurrent winner remains unknown",
			commitID:     "c2",
			headCommitID: "winner",
			parents:      map[string]string{"c2": "c1", "winner": "c1", "c1": ""},
			want:         publishedBlockReferenceRepairCommitUnknown,
		},
		{
			name:         "unrelated head is unknown",
			commitID:     "c2",
			headCommitID: "other",
			parents:      map[string]string{"c2": "c1", "other": "other-root", "other-root": ""},
			want:         publishedBlockReferenceRepairCommitUnknown,
		},
		{
			name:         "incomplete ancestry is unknown",
			commitID:     "c2",
			headCommitID: "other",
			parents:      map[string]string{"c2": "c1"},
			want:         publishedBlockReferenceRepairCommitUnknown,
			wantErr:      true,
		},
		{
			name:     "empty head is unknown",
			commitID: "c2",
			parents:  map[string]string{"c2": "c1", "c1": ""},
			want:     publishedBlockReferenceRepairCommitUnknown,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outcome, err := classifyPublishedBlockReferenceRepairCommitOutcome(tt.commitID, tt.headCommitID, func(commitID string) (string, error) {
				parent, ok := tt.parents[commitID]
				if !ok {
					return "", gocql.ErrNotFound
				}
				return parent, nil
			})
			if outcome != tt.want {
				t.Fatalf("outcome = %v, want %v", outcome, tt.want)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestPublishedBlockReferenceRepairAuthorityReadsAreColdAndStrong(t *testing.T) {
	raw, err := os.ReadFile("publish_repair.go")
	if err != nil {
		t.Fatalf("read publish_repair.go: %v", err)
	}
	source := string(raw)
	headStart := strings.Index(source, "var publishedBlockReferenceRepairHeadCommitFn")
	parentStart := strings.Index(source, "var publishedBlockReferenceRepairCommitParentFn")
	if headStart < 0 || parentStart <= headStart {
		t.Fatal("could not locate canonical repair HEAD lookup")
	}
	headSource := source[headStart:parentStart]
	if !strings.Contains(headSource, "FROM libraries WHERE org_id = ? AND library_id = ?") {
		t.Fatal("repair HEAD lookup must use the canonical org-scoped libraries row")
	}
	if !strings.Contains(headSource, ".Consistency(gocql.Serial)") {
		t.Fatal("repair HEAD lookup must settle the canonical HEAD in the SERIAL domain")
	}
	parentEnd := strings.Index(source[parentStart:], "func classifyPublishedBlockReferenceRepairCommitOutcome")
	if parentEnd < 0 {
		t.Fatal("could not locate repair parent lookup boundary")
	}
	parentSource := source[parentStart : parentStart+parentEnd]
	if !strings.Contains(parentSource, ".Consistency(gocql.EachQuorum)") {
		t.Fatal("repair ancestry lookup must use EachQuorum in the cold path")
	}
}

func TestPublishedBlockReferenceRepairSettlementAndRetryUseGlobalSerialLWT(t *testing.T) {
	raw, err := os.ReadFile("publish_repair.go")
	if err != nil {
		t.Fatalf("read publish_repair.go: %v", err)
	}
	source := string(raw)
	tests := []struct {
		name      string
		start     string
		end       string
		statement string
	}{
		{
			name:      "settlement delete",
			start:     "var deletePublishedBlockReferenceRepairFn",
			end:       "// schedulePublishedBlockReferenceRepairRetryFn",
			statement: "DELETE FROM published_block_reference_repairs",
		},
		{
			name:      "retry update",
			start:     "var schedulePublishedBlockReferenceRepairRetryFn",
			end:       "var listPublishedBlockReferenceRepairsForBucketFn",
			statement: "UPDATE published_block_reference_repairs",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := strings.Index(source, tt.start)
			end := strings.Index(source, tt.end)
			if start < 0 || end <= start {
				t.Fatalf("could not locate %s helper", tt.name)
			}
			helperSource := source[start:end]
			if !strings.Contains(helperSource, tt.statement) {
				t.Fatalf("%s must target published_block_reference_repairs", tt.name)
			}
			if !strings.Contains(helperSource, "IF EXISTS") {
				t.Fatalf("%s must remain conditional", tt.name)
			}
			if !strings.Contains(helperSource, "SerialConsistency(gocql.Serial)") {
				t.Fatalf("%s must pin the global SERIAL domain", tt.name)
			}
			if !strings.Contains(helperSource, "MapScanCAS") {
				t.Fatalf("%s must consume the conditional result with MapScanCAS", tt.name)
			}
			if strings.Contains(helperSource, ".Exec()") {
				t.Fatalf("%s must not use ordinary Exec for the LWT", tt.name)
			}
		})
	}
}

func TestPublishedBlockReferenceRepairNeverUsesLeaseExpiryAsCleanupAuthority(t *testing.T) {
	raw, err := os.ReadFile("publish_repair.go")
	if err != nil {
		t.Fatalf("read publish_repair.go: %v", err)
	}
	source := string(raw)
	start := strings.Index(source, "func repairPublishedBlockReferenceRepair")
	end := strings.Index(source[start:], "func runPendingPublishedFSObjectOwnerSweep")
	if start < 0 || end < 0 {
		t.Fatal("could not locate queued repair settlement function")
	}
	settlementSource := source[start : start+end]
	if strings.Contains(settlementSource, "LeaseExpiresAt") || strings.Contains(settlementSource, "publishedBlockReferenceRepairPreCASLease") || strings.Contains(settlementSource, "ShouldDeferCleanup") {
		t.Fatal("lease age must never decide queued repair cleanup")
	}
	if strings.Contains(settlementSource, "publishedBlockReferenceRepairCommitDefinitelyNotPublished") || strings.Contains(settlementSource, "CleanupFailedPublishArtifacts") || !strings.Contains(settlementSource, "default:") || !strings.Contains(settlementSource, "retain queued repair") {
		t.Fatal("queued repair must be fail-closed and retain every non-reachable outcome")
	}
}
