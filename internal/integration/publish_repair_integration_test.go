//go:build integration

package integration

import (
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	v2api "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

type publishRepairIntegrationFileState struct {
	orgID            string
	headCommitID     string
	fsID             string
	internalBlockIDs []string
}

const w2PostHeadEvidenceEnv = "SESAMEFS_REQUIRE_W2_POST_HEAD_EVIDENCE"

type w2PostHeadEvidenceState struct {
	normalSuccess                bool
	crashAfterAppliedHead        bool
	ambiguousCASApplied          bool
	ambiguousConfirmationUnknown bool
	leaseExpiryIsNotAuthority    bool
	casLoserCleanup              bool
	preHeadRepairRace            bool
	reachableAncestor            bool
	restartReplay                bool
}

var w2PostHeadEvidence w2PostHeadEvidenceState

func (e w2PostHeadEvidenceState) complete() bool {
	return e.normalSuccess &&
		e.crashAfterAppliedHead &&
		e.ambiguousCASApplied &&
		e.ambiguousConfirmationUnknown &&
		e.leaseExpiryIsNotAuthority &&
		e.casLoserCleanup &&
		e.preHeadRepairRace &&
		e.reachableAncestor &&
		e.restartReplay
}

func (e w2PostHeadEvidenceState) missing() []string {
	missing := make([]string, 0, 9)
	if !e.normalSuccess {
		missing = append(missing, "normal_success")
	}
	if !e.crashAfterAppliedHead {
		missing = append(missing, "crash_after_applied_head")
	}
	if !e.ambiguousCASApplied {
		missing = append(missing, "ambiguous_cas_applied")
	}
	if !e.ambiguousConfirmationUnknown {
		missing = append(missing, "ambiguous_confirmation_unavailable_retains")
	}
	if !e.leaseExpiryIsNotAuthority {
		missing = append(missing, "lease_expiry_is_not_authority")
	}
	if !e.casLoserCleanup {
		missing = append(missing, "cas_loser_cleanup")
	}
	if !e.preHeadRepairRace {
		missing = append(missing, "pre_head_repair_race")
	}
	if !e.reachableAncestor {
		missing = append(missing, "reachable_ancestor")
	}
	if !e.restartReplay {
		missing = append(missing, "restart_replay")
	}
	return missing
}

func markW2PostHeadEvidence(t *testing.T, leg string) {
	t.Helper()
	if os.Getenv(w2PostHeadEvidenceEnv) != "1" {
		return
	}
	switch leg {
	case "normal_success":
		w2PostHeadEvidence.normalSuccess = true
	case "crash_after_applied_head":
		w2PostHeadEvidence.crashAfterAppliedHead = true
	case "ambiguous_cas_applied":
		w2PostHeadEvidence.ambiguousCASApplied = true
	case "ambiguous_confirmation_unavailable_retains":
		w2PostHeadEvidence.ambiguousConfirmationUnknown = true
	case "lease_expiry_is_not_authority":
		w2PostHeadEvidence.leaseExpiryIsNotAuthority = true
	case "cas_loser_cleanup":
		w2PostHeadEvidence.casLoserCleanup = true
	case "pre_head_repair_race":
		w2PostHeadEvidence.preHeadRepairRace = true
	case "reachable_ancestor":
		w2PostHeadEvidence.reachableAncestor = true
	case "restart_replay":
		w2PostHeadEvidence.restartReplay = true
	default:
		t.Fatalf("unknown W2 evidence leg %q", leg)
	}
}

func TestPublishedBlockReferenceRepairWorker_ReplaysReachableQueuedRepairAfterRestart(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-publish-repair-replay-%d", time.Now().UnixNano()))
	fileName := "repair-replay.txt"
	fileContent := fmt.Sprintf("repair replay content %d\n", time.Now().UnixNano())

	uploadURL := getUploadLink(t, adminClient, repoID, "/")
	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", fileContent)

	state := publishRepairIntegrationReadFileState(t, repoID, "/", fileName)
	if len(state.internalBlockIDs) != 1 {
		t.Fatalf("internalBlockIDs = %v, want exactly one block for focused repair replay test", state.internalBlockIDs)
	}

	fsReferrer := dbpkg.BlockReferrerForFSObject(repoID, state.fsID)
	pubReferrer := dbpkg.BlockReferrerForPublishAttempt(state.headCommitID)
	for _, blockID := range state.internalBlockIDs {
		if err := database.RemoveBlockReference(state.orgID, blockID, fsReferrer); err != nil {
			t.Fatalf("failed to remove fs ref %q for block %s: %v", fsReferrer, blockID, err)
		}
		if err := database.AddBlockReference(state.orgID, blockID, pubReferrer, repoID, 0); err != nil {
			t.Fatalf("failed to add pub ref %q for block %s: %v", pubReferrer, blockID, err)
		}
	}
	if err := v2api.QueuePublishedFSObjectBlockReferenceRepair(database, state.orgID, repoID, state.headCommitID, state.fsID, state.internalBlockIDs); err != nil {
		t.Fatalf("failed to queue durable publish repair: %v", err)
	}

	bucket := publishRepairIntegrationBucket(state.orgID, repoID, state.headCommitID, state.fsID)
	staleCreatedAt := time.Now().UTC().Add(-time.Minute)
	leaseExpiresAt := time.Now().UTC().Add(4 * time.Minute)
	if err := database.Session().Query(`
		UPDATE published_block_reference_repairs
		SET created_at = ?, lease_expires_at = ?
		WHERE bucket = ? AND org_id = ? AND repo_id = ? AND commit_id = ? AND fs_id = ?
	`, staleCreatedAt, leaseExpiresAt, bucket, state.orgID, repoID, state.headCommitID, state.fsID).Exec(); err != nil {
		t.Fatalf("failed to backdate queued publish repair row: %v", err)
	}

	t.Cleanup(func() {
		_ = database.Session().Query(`
			DELETE FROM published_block_reference_repairs
			WHERE bucket = ? AND org_id = ? AND repo_id = ? AND commit_id = ? AND fs_id = ?
		`, bucket, state.orgID, repoID, state.headCommitID, state.fsID).Exec()
		for _, blockID := range state.internalBlockIDs {
			_ = database.RemoveBlockReference(state.orgID, blockID, pubReferrer)
			_ = database.AddBlockReference(state.orgID, blockID, fsReferrer, repoID, 0)
		}
	})

	publishRepairIntegrationAssertReferrers(t, repoID, "/", fileName, func(referrers []string) {
		if !publishRepairIntegrationHasReferrer(referrers, pubReferrer) {
			t.Fatalf("expected seeded pub ref %q before replay, got %v", pubReferrer, referrers)
		}
		if publishRepairIntegrationHasReferrer(referrers, fsReferrer) {
			t.Fatalf("expected fs ref %q to be removed before replay, got %v", fsReferrer, referrers)
		}
	})
	if !publishRepairIntegrationRepairRowExists(t, bucket, state.orgID, repoID, state.headCommitID, state.fsID) {
		t.Fatal("queued publish repair row missing before worker start")
	}

	v2api.StartPublishedBlockReferenceRepairer(database)

	if !pollUntil(t, 10*time.Second, 100*time.Millisecond, func() bool {
		referrers := uploadedFileBlockReferrers(t, repoID, "/", fileName)
		return publishRepairIntegrationHasReferrer(referrers, fsReferrer) &&
			!publishRepairIntegrationHasReferrer(referrers, pubReferrer) &&
			!publishRepairIntegrationRepairRowExists(t, bucket, state.orgID, repoID, state.headCommitID, state.fsID)
	}) {
		referrers := uploadedFileBlockReferrers(t, repoID, "/", fileName)
		t.Fatalf("timed out waiting for durable publish replay; referrers=%v rowExists=%v", referrers, publishRepairIntegrationRepairRowExists(t, bucket, state.orgID, repoID, state.headCommitID, state.fsID))
	}
	markW2PostHeadEvidence(t, "crash_after_applied_head")
	markW2PostHeadEvidence(t, "restart_replay")
}

func TestW2CreateFilePostHeadEvidenceAgainstRealCassandra(t *testing.T) {
	if os.Getenv(w2PostHeadEvidenceEnv) != "1" {
		t.Skipf("%s is not enabled", w2PostHeadEvidenceEnv)
	}
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-w2-post-head-%d", time.Now().UnixNano()))
	upload := func(fileName string) publishRepairIntegrationFileState {
		uploadURL := getUploadLink(t, adminClient, repoID, "/")
		uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", fmt.Sprintf("W2 post-head evidence %s %d\n", fileName, time.Now().UnixNano()))
		return publishRepairIntegrationReadFileState(t, repoID, "/", fileName)
	}

	normal := upload("w2-normal.txt")
	normalPub := dbpkg.BlockReferrerForPublishAttempt(normal.headCommitID)
	normalRefs := uploadedFileBlockReferrers(t, repoID, "/", "w2-normal.txt")
	if !publishRepairIntegrationHasReferrer(normalRefs, dbpkg.BlockReferrerForFSObject(repoID, normal.fsID)) || publishRepairIntegrationHasReferrer(normalRefs, normalPub) {
		t.Fatalf("normal CreateFileFromBlocks publication did not converge: %v", normalRefs)
	}
	markW2PostHeadEvidence(t, "normal_success")

	crash := upload("w2-crash-after-head.txt")
	publishRepairIntegrationSeedQueuedRepair(t, database, repoID, crash, crash.headCommitID, time.Now().UTC().Add(-time.Minute), time.Now().UTC().Add(4*time.Minute), true)
	if err := v2api.RepairPublishedFSObjectBlockReferenceRepair(database, crash.orgID, repoID, crash.headCommitID, crash.fsID, crash.internalBlockIDs); err != nil {
		t.Fatalf("repair after an applied HEAD returned error: %v", err)
	}
	assertW2StateConverged(t, repoID, "w2-crash-after-head.txt", crash, crash.headCommitID)

	ambiguousApplied := upload("w2-ambiguous-applied.txt")
	publishRepairIntegrationSeedQueuedRepair(t, database, repoID, ambiguousApplied, ambiguousApplied.headCommitID, time.Now().UTC(), time.Now().UTC().Add(5*time.Minute), true)
	restore, err := v2api.SetPublishedBlockReferenceRepairOutcomeForIntegration("reachable", nil)
	if err != nil {
		t.Fatalf("install applied ambiguous-CAS evidence hook: %v", err)
	}
	if err := v2api.RepairPublishedFSObjectBlockReferenceRepair(database, ambiguousApplied.orgID, repoID, ambiguousApplied.headCommitID, ambiguousApplied.fsID, ambiguousApplied.internalBlockIDs); err != nil {
		t.Fatalf("ambiguous CAS known-applied repair returned error: %v", err)
	}
	restore()
	assertW2StateConverged(t, repoID, "w2-ambiguous-applied.txt", ambiguousApplied, ambiguousApplied.headCommitID)
	markW2PostHeadEvidence(t, "ambiguous_cas_applied")

	ambiguousUnknown := upload("w2-ambiguous-unknown.txt")
	publishRepairIntegrationSeedQueuedRepair(t, database, repoID, ambiguousUnknown, ambiguousUnknown.headCommitID, time.Now().UTC(), time.Now().UTC().Add(-time.Minute), true)
	restore, err = v2api.SetPublishedBlockReferenceRepairOutcomeForIntegration("unknown", errors.New("confirmation unavailable"))
	if err != nil {
		t.Fatalf("install unavailable-confirmation evidence hook: %v", err)
	}
	err = v2api.RepairPublishedFSObjectBlockReferenceRepair(database, ambiguousUnknown.orgID, repoID, ambiguousUnknown.headCommitID, ambiguousUnknown.fsID, ambiguousUnknown.internalBlockIDs)
	restore()
	if err == nil || !strings.Contains(err.Error(), "confirmation unavailable") {
		t.Fatalf("ambiguous confirmation should retain repair, error=%v", err)
	}
	unknownRefs := uploadedFileBlockReferrers(t, repoID, "/", "w2-ambiguous-unknown.txt")
	unknownPub := dbpkg.BlockReferrerForPublishAttempt(ambiguousUnknown.headCommitID)
	if publishRepairIntegrationHasReferrer(unknownRefs, dbpkg.BlockReferrerForFSObject(repoID, ambiguousUnknown.fsID)) || !publishRepairIntegrationHasReferrer(unknownRefs, unknownPub) {
		t.Fatalf("unknown publication outcome changed refs: %v", unknownRefs)
	}
	if !publishRepairIntegrationRepairRowExists(t, publishRepairIntegrationBucket(ambiguousUnknown.orgID, repoID, ambiguousUnknown.headCommitID, ambiguousUnknown.fsID), ambiguousUnknown.orgID, repoID, ambiguousUnknown.headCommitID, ambiguousUnknown.fsID) {
		t.Fatal("unknown publication outcome deleted the durable repair row")
	}
	markW2PostHeadEvidence(t, "ambiguous_confirmation_unavailable_retains")
	markW2PostHeadEvidence(t, "lease_expiry_is_not_authority")

	ancestor := upload("w2-reachable-ancestor.txt")
	_ = upload("w2-newer-head.txt")
	publishRepairIntegrationSeedQueuedRepair(t, database, repoID, ancestor, ancestor.headCommitID, time.Now().UTC(), time.Now().UTC().Add(5*time.Minute), true)
	if err := v2api.RepairPublishedFSObjectBlockReferenceRepair(database, ancestor.orgID, repoID, ancestor.headCommitID, ancestor.fsID, ancestor.internalBlockIDs); err != nil {
		t.Fatalf("reachable ancestor repair returned error: %v", err)
	}
	assertW2StateConverged(t, repoID, "w2-reachable-ancestor.txt", ancestor, ancestor.headCommitID)
	markW2PostHeadEvidence(t, "reachable_ancestor")

	loser := upload("w2-loser-isolation.txt")
	fakeCommitID := fmt.Sprintf("w2-cas-loser-%d", time.Now().UnixNano())
	publishRepairIntegrationSeedQueuedRepair(t, database, repoID, loser, fakeCommitID, time.Now().UTC(), time.Now().UTC().Add(5*time.Minute), false)
	if err := v2api.CleanupFailedPublishArtifacts(database, loser.orgID, repoID, fakeCommitID, fakeCommitID, []string{loser.fsID}, loser.internalBlockIDs); err != nil {
		t.Fatalf("synchronous CAS-loser cleanup returned error: %v", err)
	}
	if err := v2api.ClearPublishedFSObjectBlockReferenceRepair(database, loser.orgID, repoID, fakeCommitID, loser.fsID); err != nil {
		t.Fatalf("clear synchronous CAS-loser repair row: %v", err)
	}
	loserRefs := uploadedFileBlockReferrers(t, repoID, "/", "w2-loser-isolation.txt")
	if !publishRepairIntegrationHasReferrer(loserRefs, dbpkg.BlockReferrerForFSObject(repoID, loser.fsID)) || publishRepairIntegrationHasReferrer(loserRefs, dbpkg.BlockReferrerForPublishAttempt(fakeCommitID)) {
		t.Fatalf("synchronous CAS-loser cleanup touched the wrong ownership set: %v", loserRefs)
	}
	if publishRepairIntegrationRepairRowExists(t, publishRepairIntegrationBucket(loser.orgID, repoID, fakeCommitID, loser.fsID), loser.orgID, repoID, fakeCommitID, loser.fsID) {
		t.Fatal("synchronous CAS-loser repair row survived exact cleanup")
	}
	markW2PostHeadEvidence(t, "cas_loser_cleanup")

	t.Run("repairRetainsPreHeadRace", func(t *testing.T) {
		handler := newBorrowedFSHeadHandler(t, database, x1StorageClass(t))
		fx := newBorrowedFSHeadFixture(t, database, handler, x1StorageClass(t))
		entered := make(chan struct{})
		release := make(chan struct{})
		var releaseOnce sync.Once
		releaseWriter := func() { releaseOnce.Do(func() { close(release) }) }
		defer releaseWriter()
		borrowedFSInstallBarriers(t, fx, func() {}, func() {}, func() {}, func() error {
			close(entered)
			<-release
			return nil
		})

		type commitResult struct {
			code int
			body string
		}
		writerResult := make(chan commitResult, 1)
		go func() {
			rec := fx.commit(t)
			writerResult <- commitResult{code: rec.Code, body: rec.Body.String()}
		}()
		select {
		case <-entered:
		case result := <-writerResult:
			t.Fatalf("writer crossed pre-HEAD barrier unexpectedly: status=%d body=%s", result.code, result.body)
		case <-time.After(20 * time.Second):
			t.Fatal("writer did not reach the pre-HEAD barrier")
		}

		commitID, fsID := publishRepairIntegrationFindQueuedRepair(t, database, fx.orgID, fx.repoID, fx.blockID)
		if commitID == "" || fsID == "" {
			t.Fatal("pre-HEAD writer did not queue a durable repair row")
		}
		err := v2api.RepairPublishedFSObjectBlockReferenceRepair(database, fx.orgID, fx.repoID, commitID, fsID, []string{fx.blockID})
		if err == nil || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("pre-HEAD repair must retain an unresolved publication, error=%v", err)
		}
		if !publishRepairIntegrationRepairRowExists(t, publishRepairIntegrationBucket(fx.orgID, fx.repoID, commitID, fsID), fx.orgID, fx.repoID, commitID, fsID) {
			t.Fatal("pre-HEAD repair deleted its durable row")
		}
		referrers := publishRepairIntegrationBlockReferrers(t, database, fx.orgID, fx.blockID)
		if !publishRepairIntegrationHasReferrer(referrers, dbpkg.BlockReferrerForPublishAttempt(commitID)) {
			t.Fatalf("pre-HEAD repair removed pub: before HEAD publication: %v", referrers)
		}
		if publishRepairIntegrationHasReferrer(referrers, dbpkg.BlockReferrerForFSObject(fx.repoID, fsID)) {
			t.Fatalf("pre-HEAD repair promoted fs: before HEAD publication: %v", referrers)
		}

		releaseWriter()
		select {
		case result := <-writerResult:
			if result.code != 200 {
				t.Fatalf("writer failed after retained repair: status=%d body=%s", result.code, result.body)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("writer did not finish after pre-HEAD repair retained its artifacts")
		}
		fx.assertHeadAdvanced(t)
		finalRefs := publishRepairIntegrationBlockReferrers(t, database, fx.orgID, fx.blockID)
		if !publishRepairIntegrationHasReferrer(finalRefs, dbpkg.BlockReferrerForFSObject(fx.repoID, fsID)) || publishRepairIntegrationHasReferrer(finalRefs, dbpkg.BlockReferrerForPublishAttempt(commitID)) {
			t.Fatalf("pre-HEAD race did not converge after writer completed: %v", finalRefs)
		}
		if publishRepairIntegrationRepairRowExists(t, publishRepairIntegrationBucket(fx.orgID, fx.repoID, commitID, fsID), fx.orgID, fx.repoID, commitID, fsID) {
			t.Fatal("pre-HEAD race left a repair row after the writer settled")
		}
		markW2PostHeadEvidence(t, "pre_head_repair_race")
	})
}

func publishRepairIntegrationSeedQueuedRepair(t *testing.T, database *dbpkg.DB, repoID string, state publishRepairIntegrationFileState, commitID string, createdAt, leaseExpiresAt time.Time, removeFSRef bool) {
	t.Helper()
	fsReferrer := dbpkg.BlockReferrerForFSObject(repoID, state.fsID)
	pubReferrer := dbpkg.BlockReferrerForPublishAttempt(commitID)
	for _, blockID := range state.internalBlockIDs {
		if removeFSRef {
			if err := database.RemoveBlockReference(state.orgID, blockID, fsReferrer); err != nil {
				t.Fatalf("remove fs ref before W2 repair seed: %v", err)
			}
		} else if err := database.AddBlockReference(state.orgID, blockID, fsReferrer, repoID, 0); err != nil {
			t.Fatalf("restore winner fs ref before W2 loser seed: %v", err)
		}
		if err := database.AddBlockReference(state.orgID, blockID, pubReferrer, repoID, 0); err != nil {
			t.Fatalf("add pub ref before W2 repair seed: %v", err)
		}
	}
	if err := v2api.QueuePublishedFSObjectBlockReferenceRepair(database, state.orgID, repoID, commitID, state.fsID, state.internalBlockIDs); err != nil {
		t.Fatalf("queue W2 repair seed: %v", err)
	}
	bucket := publishRepairIntegrationBucket(state.orgID, repoID, commitID, state.fsID)
	if err := database.Session().Query(`
		UPDATE published_block_reference_repairs
		SET created_at = ?, lease_expires_at = ?
		WHERE bucket = ? AND org_id = ? AND repo_id = ? AND commit_id = ? AND fs_id = ?
	`, createdAt, leaseExpiresAt, bucket, state.orgID, repoID, commitID, state.fsID).Exec(); err != nil {
		t.Fatalf("update W2 repair seed timestamps: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Session().Query(`
			DELETE FROM published_block_reference_repairs
			WHERE bucket = ? AND org_id = ? AND repo_id = ? AND commit_id = ? AND fs_id = ?
		`, bucket, state.orgID, repoID, commitID, state.fsID).Exec()
		for _, blockID := range state.internalBlockIDs {
			_ = database.RemoveBlockReference(state.orgID, blockID, pubReferrer)
			_ = database.AddBlockReference(state.orgID, blockID, fsReferrer, repoID, 0)
		}
	})
}

func publishRepairIntegrationFindQueuedRepair(t *testing.T, database *dbpkg.DB, orgID, repoID, blockID string) (string, string) {
	t.Helper()
	for bucket := 0; bucket < 32; bucket++ {
		iter := database.Session().Query(`
			SELECT org_id, repo_id, commit_id, fs_id
			FROM published_block_reference_repairs
			WHERE bucket = ?
		`, bucket).Iter()
		var rowOrgID, rowRepoID, commitID, fsID string
		for iter.Scan(&rowOrgID, &rowRepoID, &commitID, &fsID) {
			if rowOrgID != orgID || rowRepoID != repoID {
				continue
			}
			var blockIDs []string
			if err := database.Session().Query(`
				SELECT staged_block_ids
				FROM published_block_reference_repairs
				WHERE bucket = ? AND org_id = ? AND repo_id = ? AND commit_id = ? AND fs_id = ?
			`, bucket, rowOrgID, rowRepoID, commitID, fsID).Scan(&blockIDs); err != nil {
				t.Fatalf("load queued repair block ids: %v", err)
			}
			for _, candidate := range blockIDs {
				if candidate == blockID {
					if err := iter.Close(); err != nil {
						t.Fatalf("close queued repair iterator: %v", err)
					}
					return commitID, fsID
				}
			}
		}
		if err := iter.Close(); err != nil {
			t.Fatalf("scan queued repairs for bucket %d: %v", bucket, err)
		}
	}
	return "", ""
}

func publishRepairIntegrationBlockReferrers(t *testing.T, database *dbpkg.DB, orgID, blockID string) []string {
	t.Helper()
	iter := database.Session().Query(`
		SELECT referrer FROM block_references WHERE org_id = ? AND block_id = ?
	`, orgID, blockID).Iter()
	var referrer string
	var referrers []string
	for iter.Scan(&referrer) {
		referrers = append(referrers, referrer)
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("list block referrers for %s/%s: %v", orgID, blockID, err)
	}
	return referrers
}

func assertW2StateConverged(t *testing.T, repoID, fileName string, state publishRepairIntegrationFileState, commitID string) {
	t.Helper()
	referrers := uploadedFileBlockReferrers(t, repoID, "/", fileName)
	if !publishRepairIntegrationHasReferrer(referrers, dbpkg.BlockReferrerForFSObject(repoID, state.fsID)) || publishRepairIntegrationHasReferrer(referrers, dbpkg.BlockReferrerForPublishAttempt(commitID)) {
		t.Fatalf("W2 settlement did not converge for %s: %v", fileName, referrers)
	}
	if publishRepairIntegrationRepairRowExists(t, publishRepairIntegrationBucket(state.orgID, repoID, commitID, state.fsID), state.orgID, repoID, commitID, state.fsID) {
		t.Fatalf("W2 settlement left a repair row for %s", fileName)
	}
}

func publishRepairIntegrationReadFileState(t *testing.T, repoID, dirPath, fileName string) publishRepairIntegrationFileState {
	t.Helper()

	orgID := resolveOrgID(t, repoID)
	session := shareProjectionDBForTest(t).Session()
	fileFSID := publishRepairIntegrationLookupFileFSID(t, repoID, dirPath, fileName)

	var externalBlockIDs []string
	if err := session.Query(`SELECT block_ids FROM fs_objects WHERE library_id = ? AND fs_id = ?`, repoID, fileFSID).Scan(&externalBlockIDs); err != nil {
		t.Fatalf("failed to load block ids for %s/%s: %v", repoID, fileFSID, err)
	}
	if len(externalBlockIDs) == 0 {
		t.Fatalf("file %s/%s has no block ids", repoID, fileFSID)
	}

	internalBlockIDs := make([]string, 0, len(externalBlockIDs))
	for _, externalBlockID := range externalBlockIDs {
		var internalBlockID string
		err := session.Query(`SELECT internal_id FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`, orgID, dbpkg.PlainBlockRepresentationID, externalBlockID).Scan(&internalBlockID)
		if err != nil {
			if errors.Is(err, gocql.ErrNotFound) {
				internalBlockID = externalBlockID
			} else {
				t.Fatalf("failed to resolve block mapping for %s/%s: %v", orgID, externalBlockID, err)
			}
		}
		internalBlockIDs = append(internalBlockIDs, internalBlockID)
	}

	var headCommitID string
	if err := session.Query(`SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, repoID).Scan(&headCommitID); err != nil {
		t.Fatalf("failed to read head commit for repo %s: %v", repoID, err)
	}
	if strings.TrimSpace(headCommitID) == "" {
		t.Fatalf("repo %s has empty head commit id", repoID)
	}

	return publishRepairIntegrationFileState{
		orgID:            orgID,
		headCommitID:     headCommitID,
		fsID:             fileFSID,
		internalBlockIDs: internalBlockIDs,
	}
}

func publishRepairIntegrationLookupFileFSID(t *testing.T, repoID, dirPath, fileName string) string {
	t.Helper()

	listResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=%s", repoID, url.QueryEscape(dirPath)))
	expectStatus(t, listResp, 200)
	listResult := responseJSON(t, listResp)
	entries, _ := listResult["dirent_list"].([]interface{})
	for _, rawEntry := range entries {
		entry, _ := rawEntry.(map[string]interface{})
		if name, _ := entry["name"].(string); name == fileName {
			if fsID, _ := entry["id"].(string); strings.TrimSpace(fsID) != "" {
				return fsID
			}
		}
	}
	t.Fatalf("file %q not found in repo=%s dir=%s", fileName, repoID, dirPath)
	return ""
}

func publishRepairIntegrationRepairRowExists(t *testing.T, bucket int, orgID, repoID, commitID, fsID string) bool {
	t.Helper()

	var storedFSID string
	err := shareProjectionDBForTest(t).Session().Query(`
		SELECT fs_id FROM published_block_reference_repairs
		WHERE bucket = ? AND org_id = ? AND repo_id = ? AND commit_id = ? AND fs_id = ?
	`, bucket, orgID, repoID, commitID, fsID).Scan(&storedFSID)
	if errors.Is(err, gocql.ErrNotFound) {
		return false
	}
	if err != nil {
		t.Fatalf("failed to read queued publish repair row: %v", err)
	}
	return storedFSID != ""
}

func publishRepairIntegrationAssertReferrers(t *testing.T, repoID, dirPath, fileName string, assertFn func([]string)) {
	t.Helper()
	assertFn(uploadedFileBlockReferrers(t, repoID, dirPath, fileName))
}

func publishRepairIntegrationHasReferrer(referrers []string, want string) bool {
	for _, referrer := range referrers {
		if referrer == want {
			return true
		}
	}
	return false
}

func publishRepairIntegrationBucket(orgID, repoID, commitID, fsID string) int {
	hasher := fnv.New32a()
	for _, part := range []string{orgID, repoID, commitID, fsID} {
		_, _ = hasher.Write([]byte(part))
		_, _ = hasher.Write([]byte{0})
	}
	return int(hasher.Sum32() % 32)
}
