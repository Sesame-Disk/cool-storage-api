package v2

import (
	"errors"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
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

func TestCleanupFailedPublishAttempt_DeletesPendingFSObjectWhenUnownedAndUnreachable(t *testing.T) {
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
		return false, nil
	}
	cleanupFailedPublishFSObjectReachableFn = func(database *db.DB, repoID, fsID string) (bool, error) {
		return false, nil
	}
	deletedFSObjects := 0
	cleanupFailedPublishDeleteFSObjectFn = func(database *db.DB, repoID, fsID string) error {
		deletedFSObjects++
		if repoID != "repo-1" || fsID != "fs-1" {
			t.Fatalf("delete fs_object args = %s/%s, want repo-1/fs-1", repoID, fsID)
		}
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
	if deletedFSObjects != 1 {
		t.Fatalf("deletedFSObjects = %d, want 1", deletedFSObjects)
	}
}

func TestCleanupFailedPublishAttempt_KeepsPendingFSObjectWhenStillOwnedOrReachable(t *testing.T) {
	tests := []struct {
		name         string
		ownersRemain bool
		reachable    bool
	}{
		{name: "another owner remains", ownersRemain: true},
		{name: "commit already reachable", reachable: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			cleanupFailedPublishDeletePendingOwnerFn = func(database *db.DB, repoID, fsID, ownerID string, createdAt time.Time) error {
				return nil
			}
			cleanupFailedPublishPendingOwnerExistsFn = func(database *db.DB, repoID, fsID string) (bool, error) {
				return tt.ownersRemain, nil
			}
			reachabilityChecks := 0
			cleanupFailedPublishFSObjectReachableFn = func(database *db.DB, repoID, fsID string) (bool, error) {
				reachabilityChecks++
				return tt.reachable, nil
			}
			deleteCalls := 0
			cleanupFailedPublishDeleteFSObjectFn = func(database *db.DB, repoID, fsID string) error {
				deleteCalls++
				return nil
			}

			err := CleanupFailedPublishAttempt(&db.DB{}, "", "repo-1", "", "", []*pendingPublishedFile{{fsID: "fs-1", cleanupOwnerID: "owner-1", cleanupCreatedAt: time.Now().UTC()}})
			if err != nil {
				t.Fatalf("CleanupFailedPublishAttempt() error = %v, want nil", err)
			}
			if deleteCalls != 0 {
				t.Fatalf("deleteCalls = %d, want 0", deleteCalls)
			}
			if tt.ownersRemain && reachabilityChecks != 0 {
				t.Fatalf("reachabilityChecks = %d, want 0 when another owner remains", reachabilityChecks)
			}
			if !tt.ownersRemain && reachabilityChecks != 1 {
				t.Fatalf("reachabilityChecks = %d, want 1", reachabilityChecks)
			}
		})
	}
}

func TestRunPendingPublishedFSObjectOwnerSweep_ReleasesOnlyStaleOwners(t *testing.T) {
	oldNow := pendingPublishedFSObjectOwnerNowFn
	oldList := listPendingPublishedFSObjectOwnersByDayFn
	oldRelease := releasePendingPublishedFileOwnerFn
	t.Cleanup(func() {
		pendingPublishedFSObjectOwnerNowFn = oldNow
		listPendingPublishedFSObjectOwnersByDayFn = oldList
		releasePendingPublishedFileOwnerFn = oldRelease
	})

	now := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	pendingPublishedFSObjectOwnerNowFn = func() time.Time { return now }
	listPendingPublishedFSObjectOwnersByDayFn = func(database *db.DB, day time.Time, bucket int) ([]db.PendingPublishedFSObjectOwner, error) {
		if !day.Equal(db.GCProjectionUTCDate(now)) || bucket != 0 {
			return nil, nil
		}
		return []db.PendingPublishedFSObjectOwner{
			{RepoID: "repo-1", FSID: "fs-stale", OwnerID: "owner-stale", CreatedAt: now.Add(-pendingPublishedFSObjectOwnerStaleAfter - time.Minute)},
			{RepoID: "repo-1", FSID: "fs-fresh", OwnerID: "owner-fresh", CreatedAt: now.Add(-time.Hour)},
		}, nil
	}
	var released []string
	releasePendingPublishedFileOwnerFn = func(database *db.DB, repoID string, pending *pendingPublishedFile) error {
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

func TestRepairPublishedFSObjectBlockReferenceRepair_PromotesReachableCommit(t *testing.T) {
	oldReachable := publishedBlockReferenceRepairCommitReachableFn
	oldLoad := loadPublishedBlockReferenceRepairPendingFileFn
	oldPromote := publishedBlockReferenceRepairPromoteFn
	oldCleanup := publishedBlockReferenceRepairCleanupFn
	oldDelete := deletePublishedBlockReferenceRepairFn
	t.Cleanup(func() {
		publishedBlockReferenceRepairCommitReachableFn = oldReachable
		loadPublishedBlockReferenceRepairPendingFileFn = oldLoad
		publishedBlockReferenceRepairPromoteFn = oldPromote
		publishedBlockReferenceRepairCleanupFn = oldCleanup
		deletePublishedBlockReferenceRepairFn = oldDelete
	})

	publishedBlockReferenceRepairCommitReachableFn = func(database *db.DB, repoID, commitID string) (bool, error) {
		return true, nil
	}
	loadPublishedBlockReferenceRepairPendingFileFn = func(database *db.DB, repoID, fsID string) (*pendingPublishedFile, error) {
		return &pendingPublishedFile{fsID: fsID, externalBlockIDs: []string{"fs-block-1"}}, nil
	}
	promoteCalls := 0
	publishedBlockReferenceRepairPromoteFn = func(helper *FSHelper, orgID, repoID, commitID string, pending *pendingPublishedFile) error {
		promoteCalls++
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
	publishedBlockReferenceRepairCleanupFn = func(database *db.DB, orgID, repoID, commitID, fsID string, blockIDs []string) error {
		t.Fatal("cleanup should not run for reachable commit")
		return nil
	}
	deleteCalls := 0
	deletePublishedBlockReferenceRepairFn = func(database *db.DB, repair publishedBlockReferenceRepair) error {
		deleteCalls++
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
}

func TestRepairPublishedFSObjectBlockReferenceRepair_CleansUnreachableCommit(t *testing.T) {
	oldReachable := publishedBlockReferenceRepairCommitReachableFn
	oldHead := publishedBlockReferenceRepairHeadCommitFn
	oldParent := publishedBlockReferenceRepairCommitParentFn
	oldLoad := loadPublishedBlockReferenceRepairPendingFileFn
	oldPromote := publishedBlockReferenceRepairPromoteFn
	oldCleanup := publishedBlockReferenceRepairCleanupFn
	oldDelete := deletePublishedBlockReferenceRepairFn
	t.Cleanup(func() {
		publishedBlockReferenceRepairCommitReachableFn = oldReachable
		publishedBlockReferenceRepairHeadCommitFn = oldHead
		publishedBlockReferenceRepairCommitParentFn = oldParent
		loadPublishedBlockReferenceRepairPendingFileFn = oldLoad
		publishedBlockReferenceRepairPromoteFn = oldPromote
		publishedBlockReferenceRepairCleanupFn = oldCleanup
		deletePublishedBlockReferenceRepairFn = oldDelete
	})

	publishedBlockReferenceRepairCommitReachableFn = func(database *db.DB, repoID, commitID string) (bool, error) {
		return false, nil
	}
	publishedBlockReferenceRepairHeadCommitFn = func(database *db.DB, repoID string) (string, error) {
		return "head-2", nil
	}
	publishedBlockReferenceRepairCommitParentFn = func(database *db.DB, repoID, commitID string) (string, error) {
		return "parent-1", nil
	}
	loadPublishedBlockReferenceRepairPendingFileFn = func(database *db.DB, repoID, fsID string) (*pendingPublishedFile, error) {
		t.Fatal("fs_object lookup should not run for unreachable commits")
		return nil, nil
	}
	publishedBlockReferenceRepairPromoteFn = func(helper *FSHelper, orgID, repoID, commitID string, pending *pendingPublishedFile) error {
		t.Fatal("promote should not run for unreachable commit")
		return nil
	}
	cleanupCalls := 0
	publishedBlockReferenceRepairCleanupFn = func(database *db.DB, orgID, repoID, commitID, fsID string, blockIDs []string) error {
		cleanupCalls++
		if fsID != "fs-1" {
			t.Fatalf("cleanup fsID = %q, want fs-1", fsID)
		}
		if len(blockIDs) != 1 || blockIDs[0] != "queued-block-1" {
			t.Fatalf("cleanup blockIDs = %#v, want []string{\"queued-block-1\"}", blockIDs)
		}
		return nil
	}
	deleteCalls := 0
	deletePublishedBlockReferenceRepairFn = func(database *db.DB, repair publishedBlockReferenceRepair) error {
		deleteCalls++
		return nil
	}

	err := RepairPublishedFSObjectBlockReferenceRepair(nil, "org-1", "repo-1", "commit-1", "fs-1", []string{"queued-block-1"})
	if err != nil {
		t.Fatalf("RepairPublishedFSObjectBlockReferenceRepair() error = %v, want nil", err)
	}
	if cleanupCalls != 1 {
		t.Fatalf("cleanupCalls = %d, want 1", cleanupCalls)
	}
	if deleteCalls != 1 {
		t.Fatalf("deleteCalls = %d, want 1", deleteCalls)
	}
}

func TestRepairPublishedFSObjectBlockReferenceRepair_DefersUnreachableCommitWhilePreCASLeaseActive(t *testing.T) {
	oldReachable := publishedBlockReferenceRepairCommitReachableFn
	oldHead := publishedBlockReferenceRepairHeadCommitFn
	oldParent := publishedBlockReferenceRepairCommitParentFn
	oldLoad := loadPublishedBlockReferenceRepairPendingFileFn
	oldPromote := publishedBlockReferenceRepairPromoteFn
	oldCleanup := publishedBlockReferenceRepairCleanupFn
	oldDelete := deletePublishedBlockReferenceRepairFn
	oldNow := publishedBlockReferenceRepairNowFn
	t.Cleanup(func() {
		publishedBlockReferenceRepairCommitReachableFn = oldReachable
		publishedBlockReferenceRepairHeadCommitFn = oldHead
		publishedBlockReferenceRepairCommitParentFn = oldParent
		loadPublishedBlockReferenceRepairPendingFileFn = oldLoad
		publishedBlockReferenceRepairPromoteFn = oldPromote
		publishedBlockReferenceRepairCleanupFn = oldCleanup
		deletePublishedBlockReferenceRepairFn = oldDelete
		publishedBlockReferenceRepairNowFn = oldNow
	})

	now := time.Date(2026, time.May, 29, 12, 0, 0, 0, time.UTC)
	publishedBlockReferenceRepairCommitReachableFn = func(database *db.DB, repoID, commitID string) (bool, error) {
		return false, nil
	}
	publishedBlockReferenceRepairNowFn = func() time.Time {
		return now
	}
	publishedBlockReferenceRepairHeadCommitFn = func(database *db.DB, repoID string) (string, error) {
		return "parent-1", nil
	}
	publishedBlockReferenceRepairCommitParentFn = func(database *db.DB, repoID, commitID string) (string, error) {
		return "parent-1", nil
	}
	loadPublishedBlockReferenceRepairPendingFileFn = func(database *db.DB, repoID, fsID string) (*pendingPublishedFile, error) {
		t.Fatal("fs_object lookup should not run for deferred unreachable commit")
		return nil, nil
	}
	publishedBlockReferenceRepairPromoteFn = func(helper *FSHelper, orgID, repoID, commitID string, pending *pendingPublishedFile) error {
		t.Fatal("promote should not run for deferred unreachable commit")
		return nil
	}
	publishedBlockReferenceRepairCleanupFn = func(database *db.DB, orgID, repoID, commitID, fsID string, blockIDs []string) error {
		t.Fatal("cleanup should not run while head still matches queued commit parent")
		return nil
	}
	deletePublishedBlockReferenceRepairFn = func(database *db.DB, repair publishedBlockReferenceRepair) error {
		t.Fatal("delete should not run while cleanup is deferred")
		return nil
	}

	repair := publishedBlockReferenceRepair{
		Bucket:         publishedBlockReferenceRepairBucket("org-1", "repo-1", "commit-1", "fs-1"),
		OrgID:          "org-1",
		RepoID:         "repo-1",
		CommitID:       "commit-1",
		FSID:           "fs-1",
		StagedBlockIDs: []string{"queued-block-1"},
		CreatedAt:      now.Add(-time.Minute),
		LeaseExpiresAt: now.Add(time.Minute),
	}
	err := repairPublishedBlockReferenceRepair(nil, repair)
	if err != nil {
		t.Fatalf("repairPublishedBlockReferenceRepair() error = %v, want nil", err)
	}
}

func TestRepairPublishedFSObjectBlockReferenceRepair_CleansExpiredPreCASLeaseAtParentHead(t *testing.T) {
	oldReachable := publishedBlockReferenceRepairCommitReachableFn
	oldHead := publishedBlockReferenceRepairHeadCommitFn
	oldParent := publishedBlockReferenceRepairCommitParentFn
	oldLoad := loadPublishedBlockReferenceRepairPendingFileFn
	oldPromote := publishedBlockReferenceRepairPromoteFn
	oldCleanup := publishedBlockReferenceRepairCleanupFn
	oldDelete := deletePublishedBlockReferenceRepairFn
	oldNow := publishedBlockReferenceRepairNowFn
	t.Cleanup(func() {
		publishedBlockReferenceRepairCommitReachableFn = oldReachable
		publishedBlockReferenceRepairHeadCommitFn = oldHead
		publishedBlockReferenceRepairCommitParentFn = oldParent
		loadPublishedBlockReferenceRepairPendingFileFn = oldLoad
		publishedBlockReferenceRepairPromoteFn = oldPromote
		publishedBlockReferenceRepairCleanupFn = oldCleanup
		deletePublishedBlockReferenceRepairFn = oldDelete
		publishedBlockReferenceRepairNowFn = oldNow
	})

	now := time.Date(2026, time.May, 29, 12, 5, 0, 0, time.UTC)
	publishedBlockReferenceRepairCommitReachableFn = func(database *db.DB, repoID, commitID string) (bool, error) {
		return false, nil
	}
	publishedBlockReferenceRepairNowFn = func() time.Time {
		return now
	}
	publishedBlockReferenceRepairHeadCommitFn = func(database *db.DB, repoID string) (string, error) {
		return "parent-1", nil
	}
	publishedBlockReferenceRepairCommitParentFn = func(database *db.DB, repoID, commitID string) (string, error) {
		return "parent-1", nil
	}
	loadPublishedBlockReferenceRepairPendingFileFn = func(database *db.DB, repoID, fsID string) (*pendingPublishedFile, error) {
		t.Fatal("fs_object lookup should not run for unreachable commit")
		return nil, nil
	}
	publishedBlockReferenceRepairPromoteFn = func(helper *FSHelper, orgID, repoID, commitID string, pending *pendingPublishedFile) error {
		t.Fatal("promote should not run for unreachable commit")
		return nil
	}
	cleanupCalls := 0
	publishedBlockReferenceRepairCleanupFn = func(database *db.DB, orgID, repoID, commitID, fsID string, blockIDs []string) error {
		cleanupCalls++
		if fsID != "fs-1" {
			t.Fatalf("cleanup fsID = %q, want fs-1", fsID)
		}
		if len(blockIDs) != 1 || blockIDs[0] != "queued-block-1" {
			t.Fatalf("cleanup blockIDs = %#v, want []string{\"queued-block-1\"}", blockIDs)
		}
		return nil
	}
	deleteCalls := 0
	deletePublishedBlockReferenceRepairFn = func(database *db.DB, repair publishedBlockReferenceRepair) error {
		deleteCalls++
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
	if err != nil {
		t.Fatalf("repairPublishedBlockReferenceRepair() error = %v, want nil", err)
	}
	if cleanupCalls != 1 {
		t.Fatalf("cleanupCalls = %d, want 1", cleanupCalls)
	}
	if deleteCalls != 1 {
		t.Fatalf("deleteCalls = %d, want 1", deleteCalls)
	}
}
