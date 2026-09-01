//go:build integration

package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	v2pkg "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/google/uuid"
)

// TestX1StrictNonoverlapCharacterization drives exported GC and writer primitives
// in the candidate order (handoff → DeleteBlockByStorageKey → Finalize) without
// changing worker.go. Each named leg records evidence independently. H RED
// (resurrected K1) still completes the leg: that is the architectural finding.
func TestX1StrictNonoverlapCharacterization(t *testing.T) {
	requireCassandra(t)
	gate := x1RequireNonoverlapEvidence(t)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)

	t.Run("writerFirst", func(t *testing.T) {
		orgID := uuid.New()
		blockID := x1BlockID("x1-A-" + uuid.NewString())
		target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
		referrer := dbpkg.BlockReferrerForUpload("x1-A-" + uuid.NewString())
		x1Cleanup(t, database, orgID, blockID, referrer)
		addR3UploadPin(t, database, orgID, blockID, referrer)
		attempt := x1Attempt(target, "A")
		x1ClaimAcquired(t, store, orgID, blockID, attempt)
		hasRefs, err := store.BlockHasReferencesGlobal(orgID, blockID)
		if err != nil || !hasRefs {
			t.Fatalf("A: EACH_QUORUM missed writer pin visible=%v err=%v", hasRefs, err)
		}
		released, err := store.ReleaseBlockClaim(orgID, blockID, attempt)
		if err != nil || released != gcpkg.BlockReleaseReleased {
			t.Fatalf("A: release = %s, %v", released, err)
		}
		x1NonoverlapEvidence.writerFirst = true
	})

	t.Run("gcFirst", func(t *testing.T) {
		orgID := uuid.New()
		blockID := x1BlockID("x1-B-" + uuid.NewString())
		target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
		operationID := "x1-B-" + uuid.NewString()
		referrer := dbpkg.BlockReferrerForUpload(operationID)
		x1Cleanup(t, database, orgID, blockID, referrer)
		attempt := x1Attempt(target, "B")
		x1ClaimAcquired(t, store, orgID, blockID, attempt)
		if hasRefs, err := store.BlockHasReferencesGlobal(orgID, blockID); err != nil || hasRefs {
			t.Fatalf("B: refs before writer = %v %v", hasRefs, err)
		}
		x1RegisterFenced(t, database, orgID, blockID, operationID, target.StorageClass, target.StorageKey)
		assertR3ProductiveUploadPinVisible(t, database, store, orgID, blockID, referrer)
		if _, err := store.ReleaseBlockClaim(orgID, blockID, attempt); err != nil {
			t.Fatalf("B: cleanup release: %v", err)
		}
		x1NonoverlapEvidence.gcFirst = true
	})

	t.Run("refBeforeZeroProof", func(t *testing.T) {
		orgID := uuid.New()
		blockID := x1BlockID("x1-C1-" + uuid.NewString())
		target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
		referrer := dbpkg.BlockReferrerForUpload("x1-C1-" + uuid.NewString())
		x1Cleanup(t, database, orgID, blockID, referrer)
		attempt := x1Attempt(target, "C1")
		x1ClaimAcquired(t, store, orgID, blockID, attempt)
		addR3UploadPin(t, database, orgID, blockID, referrer)
		hasRefs, err := store.BlockHasReferencesGlobal(orgID, blockID)
		if err != nil || !hasRefs {
			t.Fatalf("C1: pin before zero-proof must revoke authorizing read; visible=%v err=%v", hasRefs, err)
		}
		if _, err := store.ReleaseBlockClaim(orgID, blockID, attempt); err != nil {
			t.Fatalf("C1: release: %v", err)
		}
		x1NonoverlapEvidence.refBeforeZeroProof = true
	})

	t.Run("refBetweenProofAndCut", func(t *testing.T) {
		orgID := uuid.New()
		blockID := x1BlockID("x1-C2-" + uuid.NewString())
		target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
		operationID := "x1-C2-" + uuid.NewString()
		referrer := dbpkg.BlockReferrerForUpload(operationID)
		x1Cleanup(t, database, orgID, blockID, referrer)
		attempt := x1Attempt(target, "C2")
		x1ClaimAcquired(t, store, orgID, blockID, attempt)
		if hasRefs, err := store.BlockHasReferencesGlobal(orgID, blockID); err != nil || hasRefs {
			t.Fatalf("C2: zero-proof = %v %v", hasRefs, err)
		}
		x1RegisterFenced(t, database, orgID, blockID, operationID, target.StorageClass, target.StorageKey)
		assertR3ProductiveUploadPinVisible(t, database, store, orgID, blockID, referrer)
		handoff, err := store.CommitBlockDeleteOrphanHandoff(orgID, blockID, attempt)
		if err != nil || (handoff.Outcome != gcpkg.BlockDeleteHandoffCommitted && handoff.Outcome != gcpkg.BlockDeleteHandoffAlreadyCommitted) {
			t.Fatalf("C2: Acquired path must still commit handoff after late up: outcome=%s err=%v", handoff.Outcome, err)
		}
		resume, err := store.ClaimBlockDelete(orgID, blockID, x1Attempt(target, "C2resume"))
		if err != nil || resume.Outcome != gcpkg.BlockClaimCommittedOwner {
			t.Fatalf("C2: resume = %s %v; want committed_owner (late up: must not take over)", resume.Outcome, err)
		}
		if resume.Owner.ClaimID != attempt.ClaimID {
			t.Fatalf("C2: stored D = %q, want %q", resume.Owner.ClaimID, attempt.ClaimID)
		}
		hasRefs, err := store.BlockHasReferencesGlobal(orgID, blockID)
		if err != nil || !hasRefs {
			t.Fatalf("C2: zombie up: should remain visible to contradiction detector; refs=%v err=%v", hasRefs, err)
		}
		t.Log("C2 current semantics: CommittedOwner + post-cut refs are a contradiction detector, not a release")
		t.Log("C2 candidate semantics: post-cut refs must not revoke D; writer already lost the fence")
		x1NonoverlapEvidence.refBetweenProofAndCut = true
	})

	t.Run("lateUploadRef", func(t *testing.T) {
		orgID := uuid.New()
		blockID := x1BlockID("x1-D1-" + uuid.NewString())
		target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
		operationID := "x1-D1-" + uuid.NewString()
		referrer := dbpkg.BlockReferrerForUpload(operationID)
		x1Cleanup(t, database, orgID, blockID, referrer)
		attempt := x1CommitHandoffAfterZeroRefs(t, store, orgID, blockID, x1Attempt(target, "D1"))
		x1RegisterFenced(t, database, orgID, blockID, operationID, target.StorageClass, target.StorageKey)
		exists, err := database.BlockReferenceExists(orgID.String(), blockID, referrer)
		if err != nil || !exists {
			t.Fatalf("D1: late up: row may exist; visible=%v err=%v", exists, err)
		}
		state, claimID, handoff, _, _ := x1ReadCommittedRow(t, database, orgID, blockID)
		if state != "deleting" || !handoff || claimID != attempt.ClaimID {
			t.Fatalf("D1: late up: revoked committed authority: state=%s handoff=%v claim=%s", state, handoff, claimID)
		}
		probe, err := database.ProbeBlockReuse(orgID.String(), blockID)
		if err != nil || probe.Decision != dbpkg.BlockReuseBlockedByGC {
			t.Fatalf("D1: new request probe = %+v %v; want BlockedByGC (no permanent reachability)", probe, err)
		}
		if x1HasFSReferrer(t, database, orgID, blockID) {
			t.Fatal("D1: late up: must not already be an fs: referrer")
		}
		t.Log("D1: post-cut up: may exist; D unrevoked; new-request BlockedByGC. Whether up: can become fs:/HEAD is not characterized")
		x1NonoverlapEvidence.lateUploadRef = true
	})

	t.Run("borrowedFSPublish", func(t *testing.T) {
		orgID := uuid.New()
		blockID := x1BlockID("x1-D2-" + uuid.NewString())
		target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
		libraryID := uuid.New().String()
		fsReferrer := dbpkg.BlockReferrerForFSObject(libraryID, uuid.New().String())
		attemptID := "x1-d2-" + uuid.NewString()
		pubReferrer := dbpkg.BlockReferrerForPublishAttempt(attemptID)
		repoID := uuid.New().String()
		x1Cleanup(t, database, orgID, blockID, fsReferrer, pubReferrer)
		if err := database.AddBlockReference(orgID.String(), blockID, fsReferrer, libraryID, 0); err != nil {
			t.Fatalf("D2: add borrowed fs: %v", err)
		}
		referrers, err := database.ListBlockReferrers(orgID.String(), blockID)
		if err != nil {
			t.Fatalf("D2: list referrers: %v", err)
		}
		hasBorrowed := false
		for _, r := range referrers {
			if r == fsReferrer {
				hasBorrowed = true
			}
		}
		if !hasBorrowed {
			t.Fatal("D2: pre-cut BorrowedFS observation missing")
		}
		if err := database.RemoveBlockReference(orgID.String(), blockID, fsReferrer); err != nil {
			t.Fatalf("D2: remove foreign fs: %v", err)
		}
		x1CommitHandoffAfterZeroRefs(t, store, orgID, blockID, x1Attempt(target, "D2"))
		if err := dbpkg.AddPublishAttemptReferences(database, orgID.String(), repoID, attemptID, []string{blockID}); err != nil {
			t.Fatalf("D2: AddPublishAttemptReferences after cut: %v", err)
		}
		pubExists, err := database.BlockReferenceExists(orgID.String(), blockID, pubReferrer)
		if err != nil {
			t.Fatalf("D2: read pub: %v", err)
		}
		if !pubExists {
			t.Fatal("D2: post-cut pub: staging must land for this characterization; subsequent HEAD is not characterized")
		}
		probe, probeErr := database.ProbeBlockReuse(orgID.String(), blockID)
		t.Logf("D2 in-flight staging pub_exists=%v new_request_probe=%v err=%v", pubExists, probe.Decision, probeErr)
		t.Log("D2 UNGUARDED: post-cut pub: staging is unguarded; writer prerequisite is not yet fully specified; subsequent HEAD is not characterized")
		if probeErr != nil || probe.Decision != dbpkg.BlockReuseBlockedByGC {
			t.Fatalf("D2: a new request must still see BlockedByGC after the cut; probe=%+v err=%v", probe, probeErr)
		}
		x1NonoverlapEvidence.borrowedFSPublish = true
	})

	t.Run("physicalDeleteFailure", func(t *testing.T) {
		storageClass := x1StorageClass(t)
		orgID := uuid.New()
		blockStore := newVerificationBlockStore(t, orgID.String())
		content := []byte("x1-E-" + uuid.NewString())
		blockID, k1 := x1SeedPhysical(t, database, blockStore, orgID, content, storageClass)
		x1Cleanup(t, database, orgID, blockID)
		target := x1Target(storageClass, k1)
		x1CommitHandoffAfterZeroRefs(t, store, orgID, blockID, x1Attempt(target, "E"))
		x1RejectForeignTenantDelete(t, k1)
		k1Exists, err := blockStore.ObjectExists(t.Context(), k1)
		if err != nil || !k1Exists {
			t.Fatalf("E: K1 must remain after failed DeleteBlockByStorageKey; exists=%v err=%v", k1Exists, err)
		}
		x1AssertCanonicalPresent(t, store, orgID, blockID, k1)
		k2, err := blockStore.MintStorageKey(blockID)
		if err != nil {
			t.Fatalf("E: mint K2: %v", err)
		}
		if k2 == k1 {
			t.Fatal("E: K2 reused K1")
		}
		installed := database.InstallBlockMetadata(t.Context(), orgID.String(), dbpkg.PlainBlockRepresentationID, blockID, "", len(content), dbpkg.BlockPhysicalLocation{StorageClass: storageClass, StorageKey: k2})
		if installed.Outcome == dbpkg.InstallBlockMetadataApplied {
			t.Fatal("E: P2 must not acquire canonical authority while P1 remains")
		}
		state, _, handoff, _, key := x1ReadCommittedRow(t, database, orgID, blockID)
		if state != "deleting" || !handoff || key != k1 {
			t.Fatalf("E: retry identity drifted: state=%s handoff=%v key=%s", state, handoff, key)
		}
		x1NonoverlapEvidence.physicalDeleteFailure = true
	})

	t.Run("postCommitResume", func(t *testing.T) {
		orgID := uuid.New()
		blockID := x1BlockID("x1-F0a-" + uuid.NewString())
		target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
		x1Cleanup(t, database, orgID, blockID)
		candidate := x1CandidateAndQueue(t, store, orgID, blockID, "hot", time.Now())
		attempt := x1CommitHandoffAfterZeroRefs(t, store, orgID, blockID, x1Attempt(target, "F0a"))
		queueExists, err := store.QueueItemExists(orgID, candidate.CandidateAt, gcpkg.ItemBlock, blockID, candidate.ItemIdentity())
		if err != nil || !queueExists {
			t.Fatalf("F0a: queue must remain after handoff (worker forbids Complete); exists=%v err=%v", queueExists, err)
		}
		resume, err := store.ClaimBlockDelete(orgID, blockID, x1Attempt(target, "F0a-other"))
		if err != nil || resume.Outcome != gcpkg.BlockClaimCommittedOwner {
			t.Fatalf("F0a: resume = %s %v; want committed_owner from blocks", resume.Outcome, err)
		}
		if resume.Owner.ClaimID != attempt.ClaimID || resume.Owner.Target != attempt.Target {
			t.Fatalf("F0a: D recovered from blocks = %+v, want %+v", resume.Owner, attempt)
		}
		x1NonoverlapEvidence.postCommitResume = true
	})

	t.Run("pendingBlocksReenqueue", func(t *testing.T) {
		orgID := uuid.New()
		blockID := x1BlockID("x1-F0b1-" + uuid.NewString())
		target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
		x1Cleanup(t, database, orgID, blockID)
		candidate := x1CandidateAndQueue(t, store, orgID, blockID, "hot", time.Now())
		x1CommitHandoffAfterZeroRefs(t, store, orgID, blockID, x1Attempt(target, "F0b1"))
		x1DeleteQueueRowKeepPending(t, database, orgID, blockID, candidate)
		queueExists, err := store.QueueItemExists(orgID, candidate.CandidateAt, gcpkg.ItemBlock, blockID, candidate.ItemIdentity())
		if err != nil || queueExists {
			t.Fatalf("F0b1: queue should be absent after exact-row delete; exists=%v err=%v", queueExists, err)
		}
		pendingExists, err := store.PendingItemExists(orgID, uuid.Nil, gcpkg.ItemBlock, blockID, candidate.ItemIdentity())
		if err != nil || !pendingExists {
			t.Fatalf("F0b1: pending should remain (scanner lock); exists=%v err=%v", pendingExists, err)
		}
		if x1FailedItemExists(t, store, orgID, blockID) {
			t.Fatal("F0b1: constructed queue-loss must not create a DLQ row")
		}
		if !x1CandidateListedOnDay(t, store, orgID, blockID, candidate.CandidateAt) {
			t.Fatal("F0b1: candidate must still be enumerable in the cursor window")
		}
		n := x1ScanOrphanedBlocksWithRestoredCursor(t, store, dbpkg.GCProjectionUTCDate(time.Now()).AddDate(0, 0, -1))
		t.Logf("F0b1: ScanOrphanedBlocksOnce enqueued=%d (global phase; this item must not reappear on queue)", n)
		queueExists, err = store.QueueItemExists(orgID, candidate.CandidateAt, gcpkg.ItemBlock, blockID, candidate.ItemIdentity())
		if err != nil || queueExists {
			t.Fatalf("F0b1: scanner must not reenqueue while pending is present; exists=%v err=%v", queueExists, err)
		}
		pendingExists, err = store.PendingItemExists(orgID, uuid.Nil, gcpkg.ItemBlock, blockID, candidate.ItemIdentity())
		if err != nil || !pendingExists {
			t.Fatalf("F0b1: pending must survive the scan; exists=%v err=%v", pendingExists, err)
		}
		if x1FailedItemExists(t, store, orgID, blockID) {
			t.Fatal("F0b1: scan must not create a DLQ row for this item")
		}
		t.Log("F0b1: queue-loss without DLQ; scanner pending lock skipped reenqueue; scanner lock ≠ recovery root")
		x1NonoverlapEvidence.pendingBlocksReenqueue = true
	})

	t.Run("candidateBehindCursor", func(t *testing.T) {
		orgID := uuid.New()
		blockID := x1BlockID("x1-F0b2-" + uuid.NewString())
		target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
		x1Cleanup(t, database, orgID, blockID)
		staleAt := time.Now().UTC().AddDate(0, 0, -30).Truncate(time.Millisecond)
		candidate, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", staleAt)
		if err != nil {
			t.Fatalf("F0b2: candidate: %v", err)
		}
		x1CommitHandoffAfterZeroRefs(t, store, orgID, blockID, x1Attempt(target, "F0b2"))
		queueExists, err := store.QueueItemExists(orgID, candidate.CandidateAt, gcpkg.ItemBlock, blockID, candidate.ItemIdentity())
		if err != nil || queueExists {
			t.Fatalf("F0b2: queue must be absent before scan; exists=%v err=%v", queueExists, err)
		}
		pendingExists, err := store.PendingItemExists(orgID, uuid.Nil, gcpkg.ItemBlock, blockID, candidate.ItemIdentity())
		if err != nil || pendingExists {
			t.Fatalf("F0b2: pending must be absent before scan; exists=%v err=%v", pendingExists, err)
		}
		if x1FailedItemExists(t, store, orgID, blockID) {
			t.Fatal("F0b2: DLQ must be absent before scan")
		}
		if !x1CandidateListedOnDay(t, store, orgID, blockID, candidate.CandidateAt) {
			t.Fatal("F0b2: candidate row still exists on its projection day")
		}
		n := x1ScanOrphanedBlocksWithRestoredCursor(t, store, dbpkg.GCProjectionUTCDate(time.Now()).AddDate(0, 0, -1))
		t.Logf("F0b2: ScanOrphanedBlocksOnce with last_candidate_day=today-1 enqueued=%d", n)
		if !x1CandidateListedOnDay(t, store, orgID, blockID, candidate.CandidateAt) {
			t.Fatal("F0b2: candidate must still exist after the scan")
		}
		queueExists, err = store.QueueItemExists(orgID, candidate.CandidateAt, gcpkg.ItemBlock, blockID, candidate.ItemIdentity())
		if err != nil || queueExists {
			t.Fatalf("F0b2: scanner must not rediscover a candidate behind the cursor; exists=%v err=%v", queueExists, err)
		}
		t.Log("F0b2: existence ≠ rediscovery; real cursor + ScanOrphanedBlocksOnce left queue absent")
		x1NonoverlapEvidence.candidateBehindCursor = true
	})

	t.Run("postDeleteCrash", func(t *testing.T) {
		storageClass := x1StorageClass(t)
		orgID := uuid.New()
		blockStore := newVerificationBlockStore(t, orgID.String())
		content := []byte("x1-F1-" + uuid.NewString())
		blockID, k1 := x1SeedPhysical(t, database, blockStore, orgID, content, storageClass)
		x1Cleanup(t, database, orgID, blockID)
		target := x1Target(storageClass, k1)
		authority := x1CommitHandoffAfterZeroRefs(t, store, orgID, blockID, x1Attempt(target, "F1"))
		if err := blockStore.DeleteBlockByStorageKey(t.Context(), k1); err != nil {
			t.Fatalf("F1: first DeleteBlockByStorageKey: %v", err)
		}
		x1AssertCanonicalPresent(t, store, orgID, blockID, k1)
		if err := blockStore.DeleteBlockByStorageKey(t.Context(), k1); err != nil {
			t.Fatalf("F1: retry DeleteBlockByStorageKey must be idempotent: %v", err)
		}
		finalized, err := store.FinalizeBlockDelete(orgID, blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(authority))
		if err != nil || finalized.Outcome != gcpkg.BlockDeleteFinalized {
			t.Fatalf("F1: exact finalize after crash = %+v %v", finalized, err)
		}
		x1AssertCanonicalAbsent(t, store, orgID, blockID)
		x1NonoverlapEvidence.postDeleteCrash = true
	})

	t.Run("ambiguousFinalizeSafety", func(t *testing.T) {
		storageClass := x1StorageClass(t)
		orgID := uuid.New()
		blockStore := newVerificationBlockStore(t, orgID.String())
		content := []byte("x1-F2s-" + uuid.NewString())
		blockID, k1 := x1SeedPhysical(t, database, blockStore, orgID, content, storageClass)
		x1Cleanup(t, database, orgID, blockID)
		target := x1Target(storageClass, k1)
		authority := x1CommitHandoffAfterZeroRefs(t, store, orgID, blockID, x1Attempt(target, "F2s"))
		if err := blockStore.DeleteBlockByStorageKey(t.Context(), k1); err != nil {
			t.Fatalf("F2-safety: delete K1: %v", err)
		}
		applied, err := store.FinalizeBlockDelete(orgID, blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(authority))
		if err != nil || applied.Outcome != gcpkg.BlockDeleteFinalized {
			t.Fatalf("F2-safety: first finalize = %+v %v", applied, err)
		}
		_ = applied
		k2, err := blockStore.MintStorageKey(blockID)
		if err != nil {
			t.Fatalf("F2-safety: mint K2: %v", err)
		}
		if k2 == k1 {
			t.Fatal("F2-safety: K2 == K1")
		}
		if _, err := blockStore.PutObjectAutoDirect(t.Context(), k2, content); err != nil {
			t.Fatalf("F2-safety: PUT K2: %v", err)
		}
		t.Cleanup(func() { _ = blockStore.DeleteBlockByStorageKey(context.Background(), k2) })
		installed := database.InstallBlockMetadata(t.Context(), orgID.String(), dbpkg.PlainBlockRepresentationID, blockID, "", len(content), dbpkg.BlockPhysicalLocation{StorageClass: storageClass, StorageKey: k2})
		if installed.Outcome != dbpkg.InstallBlockMetadataApplied {
			t.Fatalf("F2-safety: P2 install after P1 finalize = %v %v", installed.Outcome, installed.Cause)
		}
		retry, retryErr := store.FinalizeBlockDelete(orgID, blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(authority))
		if retry.Outcome == gcpkg.BlockDeleteFinalized {
			t.Fatalf("F2-safety: stale D1 must not re-apply finalize against P2: %+v %v", retry, retryErr)
		}
		if err := blockStore.DeleteBlockByStorageKey(t.Context(), k1); err != nil {
			t.Fatalf("F2-safety: retry delete K1: %v", err)
		}
		k2Exists, err := blockStore.ObjectExists(t.Context(), k2)
		if err != nil || !k2Exists {
			t.Fatalf("F2-safety: D1 destroyed K2; exists=%v err=%v", k2Exists, err)
		}
		t.Logf("F2-safety GREEN: retry finalize outcome=%s; K2 intact", retry.Outcome)
		x1NonoverlapEvidence.ambiguousFinalizeSafety = true
	})

	t.Run("ambiguousFinalizeConvergence", func(t *testing.T) {
		storageClass := x1StorageClass(t)
		orgID := uuid.New()
		blockStore := newVerificationBlockStore(t, orgID.String())
		content := []byte("x1-F2c-" + uuid.NewString())
		blockID, k1 := x1SeedPhysical(t, database, blockStore, orgID, content, storageClass)
		x1Cleanup(t, database, orgID, blockID)
		candidate := x1CandidateAndQueue(t, store, orgID, blockID, storageClass, time.Now())
		target := x1Target(storageClass, k1)
		authority := x1CommitHandoffAfterZeroRefs(t, store, orgID, blockID, x1Attempt(target, "F2c"))
		if err := blockStore.DeleteBlockByStorageKey(t.Context(), k1); err != nil {
			t.Fatalf("F2-convergence: delete: %v", err)
		}
		if _, err := store.FinalizeBlockDelete(orgID, blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(authority)); err != nil {
			t.Fatalf("F2-convergence: finalize: %v", err)
		}
		retry, err := store.FinalizeBlockDelete(orgID, blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(authority))
		t.Logf("F2-convergence: retry without orphan/020 outcome=%s err=%v (NotAuthority is classification, not a license to delete)", retry.Outcome, err)
		if retry.Outcome != gcpkg.BlockDeleteNotAuthority {
			t.Fatalf("F2-convergence: expected NotAuthority without lifecycle certificate, got %s err=%v", retry.Outcome, err)
		}
		claim, err := store.ClaimBlockDelete(orgID, blockID, x1Attempt(target, "F2c-retry"))
		if err != nil {
			t.Fatalf("F2-convergence: claim after finalize: %v", err)
		}
		if claim.Outcome != gcpkg.BlockClaimMissing {
			t.Fatalf("F2-convergence: claim after lost finalize response = %s, want missing", claim.Outcome)
		}
		pendingExists, err := store.PendingItemExists(orgID, uuid.Nil, gcpkg.ItemBlock, blockID, candidate.ItemIdentity())
		if err != nil {
			t.Fatalf("F2-convergence: pending: %v", err)
		}
		if !pendingExists {
			t.Fatal("F2-convergence: leftover pending=true is the OPEN settlement observation")
		}
		t.Logf("F2-convergence OPEN: retry=%s claim=%s pending=%v; 020 is not required for F2-safety", retry.Outcome, claim.Outcome, pendingExists)
		x1NonoverlapEvidence.ambiguousFinalizeConvergence = true
	})

	t.Run("lateRepairPut", func(t *testing.T) {
		storageClass := x1StorageClass(t)
		orgID := uuid.New()
		blockStore := newVerificationBlockStore(t, orgID.String())
		content := []byte("x1-H-" + uuid.NewString())
		blockID, k1 := x1SeedPhysical(t, database, blockStore, orgID, content, storageClass)
		x1Cleanup(t, database, orgID, blockID)
		target := v2pkg.BlockMaterializationTarget{Store: blockStore, StorageClass: storageClass, StorageKey: k1}
		entered := make(chan struct{})
		release := make(chan struct{})
		var putOnce sync.Once
		errCh := make(chan error, 1)
		go func() {
			_, err := v2pkg.PutBlockMaterializationTarget(t.Context(), database, orgID.String(), blockID, target, content, func(ctx context.Context, s *storage.BlockStore, key string, data []byte) (string, error) {
				putOnce.Do(func() { close(entered) })
				<-release
				return s.PutObjectAutoDirect(ctx, key, data)
			}, nil)
			errCh <- err
		}()
		select {
		case <-entered:
		case err := <-errCh:
			t.Fatalf("H: writer exited before put barrier: %v", err)
		case <-time.After(20 * time.Second):
			t.Fatal("H: timed out waiting for residual authority->PUT window")
		}
		attempt := x1CommitHandoffAfterZeroRefs(t, store, orgID, blockID, x1Attempt(x1Target(storageClass, k1), "H"))
		if err := blockStore.DeleteBlockByStorageKey(t.Context(), k1); err != nil {
			t.Fatalf("H: GC DeleteBlockByStorageKey: %v", err)
		}
		gone, err := blockStore.ObjectExists(t.Context(), k1)
		if err != nil || gone {
			t.Fatalf("H: K1 should be absent after GC delete; exists=%v err=%v", gone, err)
		}
		close(release)
		putErr := <-errCh
		exists, err := blockStore.ObjectExists(t.Context(), k1)
		if err != nil {
			t.Fatalf("H: ObjectExists after writer PUT: %v", err)
		}
		t.Logf("H residual PUT err=%v resurrected=%v claim=%s", putErr, exists, attempt.ClaimID)
		if !exists {
			t.Fatal("H: expected residual authorized PUT to resurrect K1 on characterization baseline")
		}
		t.Log("H PREREQUISITE: pre-PUT authority is not backed by own liveness visible to GC; need own pin → fence → existing-P PUT")
		if putErr != nil {
			t.Logf("H: PUT returned error but object exists: %v", putErr)
		}
		x1NonoverlapEvidence.lateRepairPut = true
	})

	t.Run("nextIncarnation", func(t *testing.T) {
		storageClass := x1StorageClass(t)
		orgID := uuid.New()
		blockStore := newVerificationBlockStore(t, orgID.String())
		content := []byte("x1-I-" + uuid.NewString())
		blockID, k1 := x1SeedPhysical(t, database, blockStore, orgID, content, storageClass)
		x1Cleanup(t, database, orgID, blockID)
		target := x1Target(storageClass, k1)
		k2early, err := blockStore.MintStorageKey(blockID)
		if err != nil {
			t.Fatalf("I: mint early K2: %v", err)
		}
		if k2early == k1 {
			t.Fatal("I: minted K2 reused K1")
		}
		authority := x1CommitHandoffAfterZeroRefs(t, store, orgID, blockID, x1Attempt(target, "I"))
		whileRetiring := database.InstallBlockMetadata(t.Context(), orgID.String(), dbpkg.PlainBlockRepresentationID, blockID, "", len(content), dbpkg.BlockPhysicalLocation{StorageClass: storageClass, StorageKey: k2early})
		if whileRetiring.Outcome == dbpkg.InstallBlockMetadataApplied {
			t.Fatal("I: P2 must not install while P1 is still canonical")
		}
		if err := blockStore.DeleteBlockByStorageKey(t.Context(), k1); err != nil {
			t.Fatalf("I: delete K1: %v", err)
		}
		finalized, err := store.FinalizeBlockDelete(orgID, blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(authority))
		if err != nil || finalized.Outcome != gcpkg.BlockDeleteFinalized {
			t.Fatalf("I: finalize = %+v %v", finalized, err)
		}
		k2, err := blockStore.MintStorageKey(blockID)
		if err != nil {
			t.Fatalf("I: mint K2: %v", err)
		}
		if k2 == k1 {
			t.Fatal("I: K2 == K1 after retirement")
		}
		if _, err := blockStore.PutObjectAutoDirect(t.Context(), k2, content); err != nil {
			t.Fatalf("I: PUT K2: %v", err)
		}
		t.Cleanup(func() { _ = blockStore.DeleteBlockByStorageKey(context.Background(), k2) })
		installed := database.InstallBlockMetadata(t.Context(), orgID.String(), dbpkg.PlainBlockRepresentationID, blockID, "", len(content), dbpkg.BlockPhysicalLocation{StorageClass: storageClass, StorageKey: k2})
		if installed.Outcome != dbpkg.InstallBlockMetadataApplied {
			t.Fatalf("I: P2 install after full retirement = %v %v", installed.Outcome, installed.Cause)
		}
		x1NonoverlapEvidence.nextIncarnation = true
	})

	gate.observed = x1NonoverlapEvidence.complete()
	t.Logf("X1_NONOVERLAP_EVIDENCE missing=%v complete=%t", x1NonoverlapEvidence.missing(), x1NonoverlapEvidence.complete())
}

var (
	x1StorageClassMu   sync.Mutex
	x1StorageClassName string
)

func x1StorageClass(t *testing.T) string {
	t.Helper()
	x1StorageClassMu.Lock()
	defer x1StorageClassMu.Unlock()
	if x1StorageClassName != "" {
		return x1StorageClassName
	}
	x1StorageClassName = discoverStorageClass(t)
	return x1StorageClassName
}

func TestX1CurrentProtocolFinalizeBeforeDeleteContrast(t *testing.T) {
	requireCassandra(t)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := x1BlockID("x1-current-" + uuid.NewString())
	target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
	x1Cleanup(t, database, orgID, blockID)
	attempt := x1CommitHandoffAfterZeroRefs(t, store, orgID, blockID, x1Attempt(target, "current"))
	published := store.StartBlockDeleteOrphan(orgID, blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(attempt), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", time.Now().UTC().Truncate(time.Millisecond))
	if published.Outcome != gcpkg.StartBlockDeleteOrphanCreated && published.Outcome != gcpkg.StartBlockDeleteOrphanSameAuthority {
		t.Fatalf("current protocol orphan = %s %v", published.Outcome, published.Cause)
	}
	t.Cleanup(func() { _ = store.DeleteS3Orphan(orgID, blockID, published.FirstSeenAt) })
	finalized, err := store.FinalizeBlockDelete(orgID, blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(attempt))
	if err != nil || finalized.Outcome != gcpkg.BlockDeleteFinalized {
		t.Fatalf("current protocol finalize before physical delete = %+v %v", finalized, err)
	}
	x1AssertCanonicalAbsent(t, store, orgID, blockID)
	t.Log("current protocol: blocks(L) is already gone before DeleteBlockByStorageKey; P2 IF NOT EXISTS can apply while K1 may still exist")
}
