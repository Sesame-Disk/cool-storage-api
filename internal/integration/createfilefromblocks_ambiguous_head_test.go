//go:build integration

package integration

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	v2api "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// This file validates PRE-EXISTING infrastructure (published_block_reference_repairs
// / the repair sweep in internal/api/v2/publish_repair.go), not new W2 protocol: it
// proves the ambiguous-HEAD-CAS branch (isAmbiguousLibraryHeadUpdateError /
// resolveLibraryHeadUpdateError, fs_helpers.go) converges correctly for the
// CreateFileFromBlocks funnel specifically, in both directions. It therefore
// uses plain requireCassandra(t), not the named-leg evidence-gate pattern.
//
// The seams under test (SetLibraryHeadAmbiguousCASForTest /
// SetLibraryHeadConfirmVisibleForTest, internal/api/v2/fs_helpers_head_cas_integration.go)
// are inert in production: the default implementations they override are
// byte-for-byte the real UpdateLibraryHead CAS query and the real SERIAL
// confirmation read.

// createFileFromBlocksAmbiguousHeadFSObjectID replicates buildFileFSObjectID's
// (internal/api/v2/fs_helpers.go, unexported) deterministic derivation so this
// package can locate the fs_object/repair row a forced-ambiguous commit staged,
// without duplicating any decision logic -- only the identity computation,
// which is a pure, documented hash of the manifest content.
func createFileFromBlocksAmbiguousHeadFSObjectID(t *testing.T, externalBlockIDs []string, size int64) string {
	t.Helper()
	fsContent := map[string]interface{}{
		"version":   1,
		"type":      1,
		"block_ids": externalBlockIDs,
		"size":      size,
	}
	raw, err := json.Marshal(fsContent)
	if err != nil {
		t.Fatalf("marshal fs content for id derivation: %v", err)
	}
	sum := sha1.Sum(raw)
	return hex.EncodeToString(sum[:])
}

func TestCreateFileFromBlocksAmbiguousCASConfirmedApplied(t *testing.T) {
	requireCassandra(t)
	database := shareProjectionDBForTest(t)
	storageClass := x1StorageClass(t)
	handler := newBorrowedFSHeadHandler(t, database, storageClass)
	fx := newSessionUploadHeadFixture(t, database, handler)

	restoreCAS := v2api.SetLibraryHeadAmbiguousCASForTest(true, gocql.RequestErrCASWriteUnknown{})
	t.Cleanup(restoreCAS)
	restoreConfirm := v2api.SetLibraryHeadConfirmVisibleForTest(true, "", nil)
	t.Cleanup(restoreConfirm)

	rec := fx.commit(t)
	if rec.Code != http.StatusOK {
		t.Fatalf("commit status=%d body=%s; want 200 (ambiguous-but-confirmed-applied is treated as success)", rec.Code, rec.Body.String())
	}
	fx.assertHeadAdvanced(t)
	if !fx.hasOwnFSReferrer(t) {
		t.Fatal("expected fs: promoted immediately -- this path returns nil from UpdateLibraryHead and never touches the repair sweep")
	}
	fx.assertPubCount(t, 0, "pub: must be cleared immediately after in-request promotion")
}

// TestCreateFileFromBlocksAmbiguousCASConfirmedLost forces the SERIAL
// confirmation read to report that a DIFFERENT commit won HEAD. This is the
// resolveLibraryHeadUpdateError case-(b) asymmetry documented in
// docs/GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md: unlike an ordinary
// ErrLibraryHeadConflict, this outcome does NOT trigger
// CleanupFailedPublishAttempt immediately, so pub:/the commit row/the
// already-queued repair row are left standing. That fix is deliberately out
// of scope here; what this test proves is that the PRE-EXISTING repair sweep
// still converges this funnel's own queued row to "cleaned up" once it
// classifies the commit as unreachable from HEAD.
func TestCreateFileFromBlocksAmbiguousCASConfirmedLost(t *testing.T) {
	requireCassandra(t)
	database := shareProjectionDBForTest(t)
	storageClass := x1StorageClass(t)
	handler := newBorrowedFSHeadHandler(t, database, storageClass)
	fx := newSessionUploadHeadFixture(t, database, handler)

	restoreCAS := v2api.SetLibraryHeadAmbiguousCASForTest(false, gocql.RequestErrCASWriteUnknown{})
	t.Cleanup(restoreCAS)
	restoreConfirm := v2api.SetLibraryHeadConfirmVisibleForTest(false, fx.headBefore, nil)
	t.Cleanup(restoreConfirm)

	rec := fx.commit(t)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("commit status=%d body=%s; want 500 (confirmed-lost is a definite, non-ErrLibraryHeadConflict failure)", rec.Code, rec.Body.String())
	}
	fx.assertHeadUnchanged(t)

	commitID := createFileFromBlocksAmbiguousHeadPubCommitID(t, database, fx.orgID, fx.blockID)
	if commitID == "" {
		t.Fatal("expected a staged pub:<commit> reference to survive the confirmed-lost outcome")
	}
	fsID := createFileFromBlocksAmbiguousHeadFSObjectID(t, []string{fx.sha1ID}, int64(len(fx.content)))
	createFileFromBlocksAmbiguousHeadAssertConverges(t, database, fx, commitID, fsID)
}

// TestCreateFileFromBlocksAmbiguousCASConfirmationUnknown is the same
// scenario but exercises the OTHER ambiguous outcome: the confirmation read
// itself fails (ErrLibraryHeadPublicationUnknown), not a confirmed loss. The
// underlying real state is identical (the CAS never applied), so the repair
// sweep's own reachability check must resolve it the same way via a
// different code path.
func TestCreateFileFromBlocksAmbiguousCASConfirmationUnknown(t *testing.T) {
	requireCassandra(t)
	database := shareProjectionDBForTest(t)
	storageClass := x1StorageClass(t)
	handler := newBorrowedFSHeadHandler(t, database, storageClass)
	fx := newSessionUploadHeadFixture(t, database, handler)

	restoreCAS := v2api.SetLibraryHeadAmbiguousCASForTest(false, gocql.RequestErrCASWriteUnknown{})
	t.Cleanup(restoreCAS)
	restoreConfirm := v2api.SetLibraryHeadConfirmVisibleForTest(false, "", errors.New("forced confirmation failure"))
	t.Cleanup(restoreConfirm)

	rec := fx.commit(t)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("commit status=%d body=%s; want 500 (confirmation-unknown is not ErrLibraryHeadConflict either)", rec.Code, rec.Body.String())
	}
	fx.assertHeadUnchanged(t)

	commitID := createFileFromBlocksAmbiguousHeadPubCommitID(t, database, fx.orgID, fx.blockID)
	if commitID == "" {
		t.Fatal("expected a staged pub:<commit> reference to survive the confirmation-unknown outcome")
	}
	fsID := createFileFromBlocksAmbiguousHeadFSObjectID(t, []string{fx.sha1ID}, int64(len(fx.content)))
	createFileFromBlocksAmbiguousHeadAssertConverges(t, database, fx, commitID, fsID)
}

// TestCreateFileFromBlocksAmbiguousCASAppliedConfirmationUnknownRepairs is the
// scenario none of the three legs above cover: the CAS genuinely APPLIES
// (applyReal=true actually runs it), but the SERIAL confirmation read itself
// then fails, so UpdateLibraryHead still returns
// ErrLibraryHeadPublicationUnknown -- the request sees a definite failure even
// though HEAD has already, for real, advanced to the new commit. This is the
// one combination that exercises the repair sweep's PROMOTE branch (as
// opposed to the cleanup branch every other leg here exercises) triggered by
// a genuine ambiguous CAS in this funnel; the pre-existing
// TestPublishedBlockReferenceRepairWorker_ReplaysReachableQueuedRepairAfterRestart
// proves the same promote branch converges, but never drives it through
// UpdateLibraryHead's ambiguous-CAS handling at all.
func TestCreateFileFromBlocksAmbiguousCASAppliedConfirmationUnknownRepairs(t *testing.T) {
	requireCassandra(t)
	database := shareProjectionDBForTest(t)
	storageClass := x1StorageClass(t)
	handler := newBorrowedFSHeadHandler(t, database, storageClass)
	fx := newSessionUploadHeadFixture(t, database, handler)

	restoreCAS := v2api.SetLibraryHeadAmbiguousCASForTest(true, gocql.RequestErrCASWriteUnknown{})
	t.Cleanup(restoreCAS)
	restoreConfirm := v2api.SetLibraryHeadConfirmVisibleForTest(false, "", errors.New("forced confirmation failure"))
	t.Cleanup(restoreConfirm)

	rec := fx.commit(t)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("commit status=%d body=%s; want 500 (applied-but-unconfirmed is still a definite failure to the caller)", rec.Code, rec.Body.String())
	}
	// Unlike ConfirmedLost/ConfirmationUnknown, the CAS genuinely applied:
	// HEAD really did advance, even though the request that caused it failed.
	fx.assertHeadAdvanced(t)
	if fx.hasOwnFSReferrer(t) {
		t.Fatal("expected fs: NOT promoted inline -- UpdateLibraryHeadFromSnapshot returned an error, so finalizeStoredUploadMetadataOnce never reaches promotePendingPublishedFiles")
	}

	commitID := createFileFromBlocksAmbiguousHeadPubCommitID(t, database, fx.orgID, fx.blockID)
	if commitID == "" {
		t.Fatal("expected a staged pub:<commit> reference to survive the applied-but-unconfirmed outcome")
	}
	fsID := createFileFromBlocksAmbiguousHeadFSObjectID(t, []string{fx.sha1ID}, int64(len(fx.content)))
	createFileFromBlocksAmbiguousHeadAssertPromotes(t, database, fx, commitID, fsID)
}

// createFileFromBlocksAmbiguousHeadAssertPromotes is the PROMOTE-side
// counterpart to createFileFromBlocksAmbiguousHeadAssertConverges: here the
// commit really is reachable from HEAD, so repairPublishedBlockReferenceRepair
// takes its "commitReachable" branch, which does not consult the lease at
// all (publishedBlockReferenceRepairShouldDeferCleanup only guards the
// unreachable/cleanup branch) -- no backdating is needed for this outcome.
// The generous timeout is the same StartPublishedBlockReferenceRepairer
// sync.Once accounting documented on createFileFromBlocksAmbiguousHeadAssertConverges.
func createFileFromBlocksAmbiguousHeadAssertPromotes(t *testing.T, database *dbpkg.DB, fx *sessionUploadHeadFixture, commitID, fsID string) {
	t.Helper()
	bucket := publishRepairIntegrationBucket(fx.orgID, fx.repoID, commitID, fsID)
	if !publishRepairIntegrationRepairRowExists(t, bucket, fx.orgID, fx.repoID, commitID, fsID) {
		t.Fatal("expected the repair row queued before HEAD was attempted to survive the applied-but-unconfirmed outcome")
	}

	v2api.StartPublishedBlockReferenceRepairer(database)

	if !pollUntil(t, 75*time.Second, time.Second, func() bool {
		return fx.hasOwnFSReferrer(t) &&
			!publishRepairIntegrationRepairRowExists(t, bucket, fx.orgID, fx.repoID, commitID, fsID) &&
			borrowedFSCountPrefix(t, database, fx.orgID, fx.blockID, "pub:") == 0
	}) {
		t.Fatalf("timed out waiting for the pre-existing repair sweep to promote commit=%s fs=%s", commitID, fsID)
	}
}

// TestCreateFileFromBlocksRejectsHeadCASAfterCommitReclaimedBySweep proves
// UpdateLibraryHead fails closed -- never attempts the HEAD CAS, never
// advances HEAD -- if the commit row it is about to publish is simply gone by
// the time it runs. An earlier revision of this function re-verified the
// commit's existence a second time, immediately before the CAS, specifically
// to shrink (not close -- see docs/CHANGELOG.md "W2 follow-up 3") the window
// in which the durable publish-repair sweep (publish_repair.go) could delete
// a live writer's own commit row out from under it after its pre-CAS lease
// expired. That re-check was removed ("W2 follow-up 4"): it never closed the
// race it targeted, cost an EACH_QUORUM round trip on every HEAD mutation,
// and became unnecessary once the sweep itself stopped deleting commits rows
// for commits it can only infer, cross-process, are unreachable (see
// publishedBlockReferenceRepairCleanupFn). This test now documents the
// remaining, always-true invariant: UpdateLibraryHead's own first read
// (`SELECT root_fs_id FROM commits ...`, needed for CalculateLibraryStats)
// already fails if the row is missing, for whatever reason, so a vanished
// commit row can never reach the CAS. It does not orchestrate a real timing
// race (that would be flaky); it uses the same beforeHead barrier every other
// test in this package uses to model a specific interleaving -- here,
// deleting the commit row immediately before HEAD is attempted.
func TestCreateFileFromBlocksRejectsHeadCASAfterCommitReclaimedBySweep(t *testing.T) {
	requireCassandra(t)
	database := shareProjectionDBForTest(t)
	storageClass := x1StorageClass(t)
	handler := newBorrowedFSHeadHandler(t, database, storageClass)
	fx := newSessionUploadHeadFixture(t, database, handler)

	var reclaimedCommitID string
	borrowedFSInstallBarriers(t, fx.borrowedFSHeadFixture,
		func() {},
		func() {},
		func() {},
		func() error {
			iter := database.Session().Query(`SELECT commit_id FROM commits WHERE library_id = ?`, fx.repoID).Iter()
			var commitID string
			for iter.Scan(&commitID) {
				if commitID != fx.headBefore {
					reclaimedCommitID = commitID
					break
				}
			}
			if err := iter.Close(); err != nil {
				t.Fatalf("list commits before reclaiming: %v", err)
			}
			if reclaimedCommitID == "" {
				t.Fatal("expected a newly staged commit distinct from headBefore")
			}
			// Models the commit row being gone by the time UpdateLibraryHead
			// runs, whatever the cause -- the sweep no longer does this itself
			// (see publishedBlockReferenceRepairCleanupFn), so this is a
			// direct simulation, not a reproduction of current production
			// behavior.
			if err := database.Session().Query(`DELETE FROM commits WHERE library_id = ? AND commit_id = ?`, fx.repoID, reclaimedCommitID).Exec(); err != nil {
				t.Fatalf("simulate the commit row being gone: %v", err)
			}
			return nil
		},
	)

	rec := fx.commit(t)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("commit status=%d body=%s; want 500 (the commit row was reclaimed immediately before HEAD)", rec.Code, rec.Body.String())
	}
	fx.assertHeadUnchanged(t)
	if fx.hasOwnFSReferrer(t) {
		t.Fatal("expected fs: NOT promoted -- the HEAD CAS must never have been attempted against a reclaimed commit")
	}

	var stillMissing string
	err := database.Session().Query(`SELECT commit_id FROM commits WHERE library_id = ? AND commit_id = ?`, fx.repoID, reclaimedCommitID).Scan(&stillMissing)
	if !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("expected the reclaimed commit row to remain absent, got err=%v", err)
	}
}

// createFileFromBlocksAmbiguousHeadPubCommitID reads the commit id straight
// off the block's own staged pub:<commit> referrer, so the test never needs
// to duplicate CreateFileFromBlocks' internal commit-id construction.
func createFileFromBlocksAmbiguousHeadPubCommitID(t *testing.T, database *dbpkg.DB, orgID, blockID string) string {
	t.Helper()
	referrers, err := database.ListBlockReferrers(orgID, blockID)
	if err != nil {
		t.Fatalf("ListBlockReferrers: %v", err)
	}
	const pubPrefix = "pub:"
	for _, referrer := range referrers {
		if strings.HasPrefix(referrer, pubPrefix) {
			return strings.TrimPrefix(referrer, pubPrefix)
		}
	}
	return ""
}

// createFileFromBlocksAmbiguousHeadAssertConverges backdates the repair row
// the production path already queued (before HEAD was ever attempted) past
// its pre-CAS lease, starts the pre-existing sweep worker, and polls for
// convergence to "cleaned up". Unlike
// TestPublishedBlockReferenceRepairWorker_ReplaysReachableQueuedRepairAfterRestart
// (which backdates created_at but sets a FUTURE lease_expires_at, because
// promotion of a reachable commit does not consult the lease at all), the
// "not reachable" cleanup path this test exercises explicitly defers while
// publishedBlockReferenceRepairShouldDeferCleanup sees an unexpired lease
// (publish_repair.go) -- so lease_expires_at must ALSO already be in the past,
// or the sweep intentionally waits out the lease before doing anything. The
// generous timeout accounts for StartPublishedBlockReferenceRepairer's
// sync.Once: whichever test in this binary calls it first gets the immediate
// sweep, and a test that loses that race must wait for the next periodic tick
// (publishedBlockReferenceRepairSweepInterval, ~1 minute) instead.
//
// This also proves the commit row itself survives (docs/CHANGELOG.md, "W2
// follow-up 4"): publishedBlockReferenceRepairCleanupFn removes the staged
// pub: reference and the repair row but deliberately never deletes commits,
// because this sweep's "unreachable" verdict is a cross-process inference
// that can be wrong about a merely-slow, still-alive writer, and destroying
// the row a live writer is about to CAS HEAD onto would corrupt the library
// permanently. Leaving it in place is safe either way: an orphaned commits
// row nothing points to is inert.
func createFileFromBlocksAmbiguousHeadAssertConverges(t *testing.T, database *dbpkg.DB, fx *sessionUploadHeadFixture, commitID, fsID string) {
	t.Helper()
	bucket := publishRepairIntegrationBucket(fx.orgID, fx.repoID, commitID, fsID)
	if !publishRepairIntegrationRepairRowExists(t, bucket, fx.orgID, fx.repoID, commitID, fsID) {
		t.Fatal("expected the repair row queued before HEAD was attempted to survive the ambiguous outcome")
	}
	if err := database.Session().Query(`
		UPDATE published_block_reference_repairs
		SET created_at = ?, lease_expires_at = ?
		WHERE bucket = ? AND org_id = ? AND repo_id = ? AND commit_id = ? AND fs_id = ?
	`, time.Now().UTC().Add(-2*time.Minute), time.Now().UTC().Add(-time.Minute), bucket, fx.orgID, fx.repoID, commitID, fsID).Exec(); err != nil {
		t.Fatalf("backdate queued repair row: %v", err)
	}

	v2api.StartPublishedBlockReferenceRepairer(database)

	if !pollUntil(t, 75*time.Second, time.Second, func() bool {
		return !publishRepairIntegrationRepairRowExists(t, bucket, fx.orgID, fx.repoID, commitID, fsID) &&
			borrowedFSCountPrefix(t, database, fx.orgID, fx.blockID, "pub:") == 0
	}) {
		t.Fatalf("timed out waiting for the pre-existing repair sweep to clean up commit=%s fs=%s", commitID, fsID)
	}
	if fx.hasOwnFSReferrer(t) {
		t.Fatal("an unreachable commit must never be promoted to fs:")
	}
	var stillPresent string
	if err := database.Session().Query(`
		SELECT commit_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, fx.repoID, commitID).Scan(&stillPresent); err != nil {
		t.Fatalf("expected the sweep's cleanup to leave the (now orphaned) commit row in place, got err=%v", err)
	}
}
