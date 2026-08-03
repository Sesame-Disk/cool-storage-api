package v2

import (
	"errors"
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
	cleanupPendingPublishedFileAttemptCommitReachableFn = func(database *db.DB, repoID, commitID string) (bool, error) {
		reachabilityChecks++
		if repoID != "repo-1" || commitID != "commit-1" {
			t.Fatalf("reachability args = %s/%s, want repo-1/commit-1", repoID, commitID)
		}
		return true, nil
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
	cleanupPendingPublishedFileAttemptCommitReachableFn = func(database *db.DB, repoID, commitID string) (bool, error) {
		reachabilityChecks++
		return false, nil
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
	publishedBlockReferenceRepairNowFn = func() time.Time {
		return now
	}
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
		t.Fatal("head lookup should not run while pre-CAS lease is active")
		return "", nil
	}
	publishedBlockReferenceRepairCommitParentFn = func(database *db.DB, repoID, commitID string) (string, error) {
		t.Fatal("parent lookup should not run while pre-CAS lease is active")
		return "", nil
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
