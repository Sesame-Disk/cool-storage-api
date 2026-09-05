//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	v2pkg "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TestSessionUploadOwnLiveness is the W2 SessionUpload-parity counterpart to
// TestBorrowedFSOwnLiveness: CreateFileFromBlocks now upserts a SessionUpload
// block's existing up:<session> reference -- renewing it when present,
// recreating it when it already lapsed -- and re-validates its exact physical
// placement immediately before HEAD, exactly like it already does for
// BorrowedFS blocks. Unlike the BorrowedFS fixture, the block here
// is seeded through a REAL /blocks/upload call (not a direct DB seed), so it
// naturally carries the up:<session> reference RegisterUploadedBlockTarget
// already writes at upload time -- there is no foreign fs: to borrow.
func TestSessionUploadOwnLiveness(t *testing.T) {
	requireCassandra(t)
	gate := sessionUploadRequireOwnLivenessEvidence(t)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	storageClass := x1StorageClass(t)
	handler := newBorrowedFSHeadHandler(t, database, storageClass)
	gin.SetMode(gin.TestMode)

	t.Run("sessionUploadRenewalVisibleBeforeHead", func(t *testing.T) {
		fx := newSessionUploadHeadFixture(t, database, handler)
		rec := fx.commit(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("sessionUploadRenewalVisibleBeforeHead: commit status=%d body=%s", rec.Code, rec.Body.String())
		}
		fx.assertHeadAdvanced(t)
		if !fx.hasOwnFSReferrer(t) {
			t.Fatal("sessionUploadRenewalVisibleBeforeHead: expected fs: after promote")
		}
		sessionUploadOwnLivenessEvidence.renewalVisibleBeforeHead = true
	})

	t.Run("sessionUploadRenewalExtendsNearExpiredTTL", func(t *testing.T) {
		fx := newSessionUploadHeadFixture(t, database, handler)
		if err := database.Session().Query(`
			UPDATE block_references USING TTL 5
			SET library_id = ?, created_at = ?
			WHERE org_id = ? AND block_id = ? AND referrer = ?
		`, fx.repoID, time.Now().UTC(), fx.orgID, fx.blockID, fx.sessionRef).Exec(); err != nil {
			t.Fatalf("sessionUploadRenewalExtendsNearExpiredTTL: shorten reference TTL: %v", err)
		}
		var canonicalExpiresAt time.Time
		var canonicalStorageClass string
		if err := database.Session().Query(
			`SELECT expires_at, storage_class FROM gc_provisional_block_refs WHERE org_id = ? AND block_id = ? AND referrer = ?`,
			fx.orgID, fx.blockID, fx.sessionRef).Scan(&canonicalExpiresAt, &canonicalStorageClass); err != nil {
			t.Fatalf("sessionUploadRenewalExtendsNearExpiredTTL: read canonical tracker: %v", err)
		}
		if err := database.Session().Query(`
			UPDATE gc_provisional_block_refs USING TTL 5
			SET storage_class = ?, expires_at = ?
			WHERE org_id = ? AND block_id = ? AND referrer = ?
		`, canonicalStorageClass, canonicalExpiresAt.UTC(), fx.orgID, fx.blockID, fx.sessionRef).Exec(); err != nil {
			t.Fatalf("sessionUploadRenewalExtendsNearExpiredTTL: shorten tracker TTL: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
		rec := fx.commit(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("sessionUploadRenewalExtendsNearExpiredTTL: commit status=%d body=%s", rec.Code, rec.Body.String())
		}
		var renewedExpiresAt time.Time
		var renewedTrackerTTL int
		if err := database.Session().Query(
			`SELECT expires_at, TTL(expires_at) FROM gc_provisional_block_refs WHERE org_id = ? AND block_id = ? AND referrer = ?`,
			fx.orgID, fx.blockID, fx.sessionRef).Scan(&renewedExpiresAt, &renewedTrackerTTL); err != nil {
			t.Fatalf("sessionUploadRenewalExtendsNearExpiredTTL: read renewed tracker: %v", err)
		}
		if renewedExpiresAt.UTC().Equal(canonicalExpiresAt.UTC()) {
			t.Fatalf("sessionUploadRenewalExtendsNearExpiredTTL: commit did not move the deadline off %v", canonicalExpiresAt.UTC())
		}
		if renewedTrackerTTL < dbpkg.ProvisionalBlockReferenceTTLSeconds-10 {
			t.Fatalf("sessionUploadRenewalExtendsNearExpiredTTL: renewed tracker TTL = %d, want refreshed ~%ds horizon", renewedTrackerTTL, dbpkg.ProvisionalBlockReferenceTTLSeconds)
		}
		var renewedReferenceTTL int
		if err := database.Session().Query(
			`SELECT TTL(created_at) FROM block_references WHERE org_id = ? AND block_id = ? AND referrer = ?`,
			fx.orgID, fx.blockID, fx.sessionRef,
		).Scan(&renewedReferenceTTL); err != nil {
			t.Fatalf("sessionUploadRenewalExtendsNearExpiredTTL: read renewed up:<session> reference: %v", err)
		}
		if renewedReferenceTTL < dbpkg.ProvisionalBlockReferenceTTLSeconds-10 {
			t.Fatalf("sessionUploadRenewalExtendsNearExpiredTTL: renewed up:<session> TTL = %d, want refreshed ~%ds horizon", renewedReferenceTTL, dbpkg.ProvisionalBlockReferenceTTLSeconds)
		}
		var staleProjection time.Time
		err := database.Session().Query(
			`SELECT expires_at FROM gc_provisional_block_refs_by_day
			 WHERE expiry_day = ? AND bucket = ? AND expires_at = ? AND org_id = ? AND block_id = ? AND referrer = ?`,
			dbpkg.GCProjectionUTCDate(canonicalExpiresAt), dbpkg.GCDiscoveryBucket(fx.orgID, fx.blockID, fx.sessionRef),
			canonicalExpiresAt.UTC(), fx.orgID, fx.blockID, fx.sessionRef,
		).Scan(&staleProjection)
		if !errors.Is(err, gocql.ErrNotFound) {
			t.Fatalf("sessionUploadRenewalExtendsNearExpiredTTL: stale expiry projection lookup = %v, want gocql.ErrNotFound", err)
		}
		fx.assertHeadAdvanced(t)
		sessionUploadOwnLivenessEvidence.renewalExtendsNearExpiredTTL = true
	})

	t.Run("sessionUploadWriterFirst", func(t *testing.T) {
		fx := newSessionUploadHeadFixture(t, database, handler)
		borrowedFSInstallBarriers(t, fx.borrowedFSHeadFixture,
			func() {},
			func() {
				attempt := x1Attempt(fx.target, "sessionupload-writer-first")
				x1ClaimAcquired(t, store, fx.orgUUID, fx.blockID, attempt)
				hasRefs, err := store.BlockHasReferencesGlobal(fx.orgUUID, fx.blockID)
				if err != nil || !hasRefs {
					t.Fatalf("sessionUploadWriterFirst: EACH_QUORUM missed up:<session> visible=%v err=%v", hasRefs, err)
				}
				released, err := store.ReleaseBlockClaim(fx.orgUUID, fx.blockID, attempt)
				if err != nil || released != gcpkg.BlockReleaseReleased {
					t.Fatalf("sessionUploadWriterFirst: release = %s, %v", released, err)
				}
			},
			func() {},
			func() error { return nil },
		)
		rec := fx.commit(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("sessionUploadWriterFirst: commit status=%d body=%s", rec.Code, rec.Body.String())
		}
		fx.assertHeadAdvanced(t)
		sessionUploadOwnLivenessEvidence.writerFirst = true
	})

	t.Run("sessionUploadGcFirst", func(t *testing.T) {
		fx := newSessionUploadHeadFixture(t, database, handler)
		var attempt gcpkg.BlockDeleteAuthority
		borrowedFSInstallBarriers(t, fx.borrowedFSHeadFixture,
			func() {
				fx.dropOwnSessionRef(t)
				attempt = x1CommitHandoffAfterZeroRefs(t, store, fx.orgUUID, fx.blockID, x1Attempt(fx.target, "sessionupload-gc-first"))
			},
			func() {
				if got := borrowedFSCountPrefix(t, database, fx.orgID, fx.blockID, "up:"); got != 1 {
					t.Fatalf("sessionUploadGcFirst: lapsed up:<session> was not recreated with the same identity, count=%d", got)
				}
				fx.assertDUnrevoked(t, attempt)
			},
			func() { fx.assertPubCount(t, 1, "sessionUploadGcFirst: pub: must be staged before HEAD") },
			func() error {
				fenced, err := database.BlockDeleteFenceActive(fx.orgID, fx.blockID)
				if err != nil {
					t.Fatalf("sessionUploadGcFirst: BlockDeleteFenceActive: %v", err)
				}
				if !fenced {
					t.Fatal("sessionUploadGcFirst: expected active fence before HEAD")
				}
				return nil
			},
		)
		rec := fx.commit(t)
		if rec.Code != http.StatusConflict {
			t.Fatalf("sessionUploadGcFirst: commit status=%d body=%s; want 409", rec.Code, rec.Body.String())
		}
		fx.assertHeadUnchanged(t)
		fx.assertDUnrevoked(t, attempt)
		if fx.hasOwnFSReferrer(t) {
			t.Fatal("sessionUploadGcFirst: unexpected fs: after fenced publication")
		}
		fx.assertPubCount(t, 0, "sessionUploadGcFirst: fence abort must drop staged pub:")
		sessionUploadOwnLivenessEvidence.gcFirst = true
	})

	t.Run("sessionUploadGcFullyRetiredBeforeRenewal", func(t *testing.T) {
		fx := newSessionUploadHeadFixture(t, database, handler)
		blockStore := newVerificationBlockStore(t, fx.orgID)
		var committed gcpkg.CommittedBlockDeleteAuthority
		var firstSeenAt time.Time
		borrowedFSInstallBarriers(t, fx.borrowedFSHeadFixture,
			func() {
				fx.dropOwnSessionRef(t)
				attempt := x1CommitHandoffAfterZeroRefs(t, store, fx.orgUUID, fx.blockID, x1Attempt(fx.target, "sessionupload-fully-retired"))
				committed = gcpkg.CommittedBlockDeleteAuthorityForTest(attempt)
				publication := store.StartBlockDeleteOrphan(fx.orgUUID, fx.blockID, committed, fx.sha1ID, time.Now().UTC())
				if publication.Outcome != gcpkg.StartBlockDeleteOrphanCreated {
					t.Fatalf("sessionUploadGcFullyRetiredBeforeRenewal: StartBlockDeleteOrphan = %s, %v", publication.Outcome, publication.Cause)
				}
				firstSeenAt = publication.FirstSeenAt
				finalized, err := store.FinalizeBlockDelete(fx.orgUUID, fx.blockID, committed)
				if err != nil || finalized.Outcome != gcpkg.BlockDeleteFinalized {
					t.Fatalf("sessionUploadGcFullyRetiredBeforeRenewal: FinalizeBlockDelete = %s, %v", finalized.Outcome, err)
				}
				if err := blockStore.DeleteBlockByStorageKey(context.Background(), fx.target.StorageKey); err != nil {
					t.Fatalf("sessionUploadGcFullyRetiredBeforeRenewal: physical delete: %v", err)
				}
				if _, err := store.TerminateBlockDeleteLifecycle(fx.orgUUID, fx.blockID, committed); err != nil {
					t.Fatalf("sessionUploadGcFullyRetiredBeforeRenewal: TerminateBlockDeleteLifecycle: %v", err)
				}
				if err := store.DeleteS3Orphan(fx.orgUUID, fx.blockID, firstSeenAt); err != nil {
					t.Fatalf("sessionUploadGcFullyRetiredBeforeRenewal: DeleteS3Orphan: %v", err)
				}
				x1AssertCanonicalAbsent(t, store, fx.orgUUID, fx.blockID)
			},
			func() {
				fenced, err := database.BlockDeleteFenceActive(fx.orgID, fx.blockID)
				if err != nil {
					t.Fatalf("sessionUploadGcFullyRetiredBeforeRenewal: BlockDeleteFenceActive: %v", err)
				}
				if fenced {
					t.Fatal("sessionUploadGcFullyRetiredBeforeRenewal: BlockDeleteFenceActive must be false after GC retires the block")
				}
			},
			func() {
				if got := borrowedFSCountPrefix(t, database, fx.orgID, fx.blockID, "up:"); got != 1 {
					t.Fatalf("sessionUploadGcFullyRetiredBeforeRenewal: lapsed up:<session> was not recreated after D/retirement, count=%d", got)
				}
				fx.assertPubCount(t, 1, "sessionUploadGcFullyRetiredBeforeRenewal: pub: must be staged before HEAD")
			},
			func() error { return nil },
		)
		rec := fx.commit(t)
		if rec.Code != http.StatusConflict {
			t.Fatalf("sessionUploadGcFullyRetiredBeforeRenewal: commit status=%d body=%s; want 409", rec.Code, rec.Body.String())
		}
		fx.assertHeadUnchanged(t)
		if fx.hasOwnFSReferrer(t) {
			t.Fatal("sessionUploadGcFullyRetiredBeforeRenewal: unexpected fs: after rejected publication")
		}
		fx.assertPubCount(t, 0, "sessionUploadGcFullyRetiredBeforeRenewal: exact-authority rejection must drop staged pub:")
		sessionUploadOwnLivenessEvidence.gcFullyRetiredBeforeRenewal = true
	})

	t.Run("sessionUploadRenewalRetryIsIdempotent", func(t *testing.T) {
		fx := newSessionUploadHeadFixture(t, database, handler)
		var beforeFirstAttempt time.Time
		if err := database.Session().Query(
			`SELECT expires_at FROM gc_provisional_block_refs WHERE org_id = ? AND block_id = ? AND referrer = ?`,
			fx.orgID, fx.blockID, fx.sessionRef).Scan(&beforeFirstAttempt); err != nil {
			t.Fatalf("sessionUploadRenewalRetryIsIdempotent: read pre-attempt deadline: %v", err)
		}
		injectFailure := true
		borrowedFSInstallBarriers(t, fx.borrowedFSHeadFixture,
			func() {}, func() {}, func() {},
			func() error {
				if injectFailure {
					injectFailure = false
					return fmt.Errorf("sessionUploadRenewalRetryIsIdempotent: injected pre-HEAD failure")
				}
				return nil
			},
		)
		rec := fx.commit(t)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("sessionUploadRenewalRetryIsIdempotent: first attempt status=%d body=%s; want 500", rec.Code, rec.Body.String())
		}
		fx.assertHeadUnchanged(t)
		var afterFirstAttempt time.Time
		if err := database.Session().Query(
			`SELECT expires_at FROM gc_provisional_block_refs WHERE org_id = ? AND block_id = ? AND referrer = ?`,
			fx.orgID, fx.blockID, fx.sessionRef).Scan(&afterFirstAttempt); err != nil {
			t.Fatalf("sessionUploadRenewalRetryIsIdempotent: read post-first deadline: %v", err)
		}
		if !afterFirstAttempt.After(beforeFirstAttempt) {
			t.Fatalf("sessionUploadRenewalRetryIsIdempotent: first renewal did not run")
		}
		time.Sleep(50 * time.Millisecond)
		retry := fx.commit(t)
		if retry.Code != http.StatusOK {
			t.Fatalf("sessionUploadRenewalRetryIsIdempotent: retry status=%d body=%s", retry.Code, retry.Body.String())
		}
		fx.assertHeadAdvanced(t)
		var afterRetry time.Time
		if err := database.Session().Query(
			`SELECT expires_at FROM gc_provisional_block_refs WHERE org_id = ? AND block_id = ? AND referrer = ?`,
			fx.orgID, fx.blockID, fx.sessionRef).Scan(&afterRetry); err != nil {
			t.Fatalf("sessionUploadRenewalRetryIsIdempotent: read post-retry deadline: %v", err)
		}
		if !afterRetry.After(afterFirstAttempt) {
			t.Fatalf("sessionUploadRenewalRetryIsIdempotent: second real renewal did not run")
		}
		if borrowedFSCountPrefix(t, database, fx.orgID, fx.blockID, "up:") != 1 || !fx.hasOwnFSReferrer(t) || borrowedFSCountPrefix(t, database, fx.orgID, fx.blockID, "fs:"+fx.repoID+":") != 1 {
			t.Fatalf("sessionUploadRenewalRetryIsIdempotent: retry duplicated liveness references")
		}
		sessionUploadOwnLivenessEvidence.renewalRetryIsIdempotent = true
	})

	gate.observed = sessionUploadOwnLivenessEvidence.complete()
	t.Logf("SESSIONUPLOAD_OWN_LIVENESS_EVIDENCE missing=%v complete=%t", sessionUploadOwnLivenessEvidence.missing(), sessionUploadOwnLivenessEvidence.complete())
}

type sessionUploadHeadFixture struct {
	*borrowedFSHeadFixture
	sessionRef string
}

func newSessionUploadHeadFixture(t *testing.T, database *dbpkg.DB, handler *v2pkg.FileHandler) *sessionUploadHeadFixture {
	t.Helper()
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-suh-%d", time.Now().UnixNano()))
	content := []byte("sessionupload-head-" + uuid.NewString())
	sessionID := webCreateBlockSession(t, adminClient, repoID, "/", int64(len(content)))
	uploadResp := webUploadBlock(t, adminClient, sessionID, content)
	if uploadResp.StatusCode != http.StatusOK && uploadResp.StatusCode != http.StatusCreated {
		body := responseBody(t, uploadResp)
		uploadResp.Body.Close()
		t.Fatalf("seed block upload status=%d body=%s", uploadResp.StatusCode, body)
	}
	uploadResp.Body.Close()
	session, ok, err := database.GetBlockUploadSession(sessionID)
	if err != nil || !ok {
		t.Fatalf("read block-upload session %s: ok=%v err=%v", sessionID, ok, err)
	}
	orgUUID, err := uuid.Parse(session.OrgID)
	if err != nil {
		t.Fatalf("parse session org_id %q: %v", session.OrgID, err)
	}
	blockID := sha256hex(content)
	externalSHA1 := sha1hex(content)
	sessionRef := dbpkg.BlockReferrerForUpload(sessionID)
	var storageKey, storageClass string
	if err := database.Session().Query(
		`SELECT storage_class, storage_key FROM blocks WHERE org_id = ? AND block_id = ?`,
		session.OrgID, blockID).Scan(&storageClass, &storageKey); err != nil {
		t.Fatalf("read seeded block placement %s/%s: %v", session.OrgID, blockID, err)
	}
	cleanupUploadedBlockArtifactsForTest(t, session.OrgID, repoID, blockID, externalSHA1, sessionRef)
	inner := &borrowedFSHeadFixture{
		database: database, handler: handler, storageClass: storageClass,
		repoID: repoID, orgID: session.OrgID, orgUUID: orgUUID,
		userID: session.UserID, sessionID: sessionID, blockID: blockID,
		sha1ID: externalSHA1, content: content,
		filename: "sessionupload-head-" + uuid.NewString()[:8] + ".txt",
		target:   x1Target(storageClass, storageKey),
	}
	inner.headBefore = borrowedFSReadHead(t, database, session.OrgID, repoID)
	if inner.headBefore == "" {
		t.Fatal("library has empty HEAD before commit")
	}
	return &sessionUploadHeadFixture{borrowedFSHeadFixture: inner, sessionRef: sessionRef}
}

// dropOwnSessionRef models the writer's only pre-existing liveness having
// lapsed before GC's zero-proof read, without changing the session manifest.
func (fx *sessionUploadHeadFixture) dropOwnSessionRef(t *testing.T) {
	t.Helper()
	if err := fx.database.RemoveBlockReference(fx.orgID, fx.blockID, fx.sessionRef); err != nil {
		t.Fatalf("drop own session ref: %v", err)
	}
}
