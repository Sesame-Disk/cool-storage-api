//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	v2pkg "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TestSessionUploadOwnLiveness is the W2 SessionUpload-parity counterpart to
// TestBorrowedFSOwnLiveness: CreateFileFromBlocks now renews (not creates) a
// SessionUpload block's existing up:<session> reference and re-validates its
// exact physical placement immediately before HEAD, exactly like it already
// does for BorrowedFS blocks. Unlike the BorrowedFS fixture, the block here
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
		// Force both TTL-bound rows near expiry, mirroring
		// TestWebBlockUploadWritesReferenceAndExpiryTogether's renewal proof: a mere
		// sleep between two full-horizon writes could leave the integer TTL
		// unchanged and would not prove the commit-path renewal refreshed either
		// deadline.
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

		// The deadline is derived from wall-clock time at millisecond resolution;
		// sleep past that boundary so commit's renewal is REQUIRED to move it.
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
			t.Fatalf("sessionUploadRenewalExtendsNearExpiredTTL: commit did not move the deadline off %v; renewal was never exercised", canonicalExpiresAt.UTC())
		}
		if renewedTrackerTTL < dbpkg.ProvisionalBlockReferenceTTLSeconds-10 {
			t.Fatalf("sessionUploadRenewalExtendsNearExpiredTTL: renewed tracker TTL = %d, want a refreshed ~%ds horizon", renewedTrackerTTL, dbpkg.ProvisionalBlockReferenceTTLSeconds)
		}
		var staleProjection time.Time
		err := database.Session().Query(
			`SELECT expires_at FROM gc_provisional_block_refs_by_day
			 WHERE expiry_day = ? AND bucket = ? AND expires_at = ? AND org_id = ? AND block_id = ? AND referrer = ?`,
			dbpkg.GCProjectionUTCDate(canonicalExpiresAt), dbpkg.GCDiscoveryBucket(fx.orgID, fx.blockID, fx.sessionRef),
			canonicalExpiresAt.UTC(), fx.orgID, fx.blockID, fx.sessionRef,
		).Scan(&staleProjection)
		if err == nil {
			t.Fatalf("sessionUploadRenewalExtendsNearExpiredTTL: renewal left the superseded projection at %v in place", canonicalExpiresAt.UTC())
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
			func() {},
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

	// sessionUploadGcFullyRetiredBeforeRenewal is the SessionUpload analog of
	// gcFullyRetiredBeforeLateOwnPin: GC runs its ENTIRE destructive lifecycle
	// (claim, zero-proof, commit D, orphan publish, Finalize, physical delete,
	// settle) to completion before the commit's renewal step even runs. A bare
	// fence check would report false (no claim row, no orphan row left) --
	// only the exact-placement check catches this.
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
					t.Fatal("sessionUploadGcFullyRetiredBeforeRenewal: BlockDeleteFenceActive must be false once GC has fully retired the block")
				}
			},
			func() {
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
		rec := fx.commit(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("sessionUploadRenewalRetryIsIdempotent: commit status=%d body=%s", rec.Code, rec.Body.String())
		}
		retry := fx.commit(t)
		if retry.Code != http.StatusOK {
			t.Fatalf("sessionUploadRenewalRetryIsIdempotent: retry status=%d body=%s", retry.Code, retry.Body.String())
		}
		if borrowedFSCountPrefix(t, database, fx.orgID, fx.blockID, "up:") != 1 || !fx.hasOwnFSReferrer(t) || borrowedFSCountPrefix(t, database, fx.orgID, fx.blockID, "fs:"+fx.repoID+":") != 1 {
			t.Fatalf("sessionUploadRenewalRetryIsIdempotent: retry duplicated liveness references")
		}
		sessionUploadOwnLivenessEvidence.renewalRetryIsIdempotent = true
	})

	gate.observed = sessionUploadOwnLivenessEvidence.complete()
	t.Logf("SESSIONUPLOAD_OWN_LIVENESS_EVIDENCE missing=%v complete=%t", sessionUploadOwnLivenessEvidence.missing(), sessionUploadOwnLivenessEvidence.complete())
}

// sessionUploadHeadFixture embeds borrowedFSHeadFixture (reusing its HTTP
// commit call and HEAD/fs:/pub: assertions unchanged -- they only depend on
// fields, not on how the block was seeded) but seeds its single block
// through a REAL /blocks/upload call, so the block naturally carries its own
// up:<session> reference instead of a foreign fs: one.
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
		database:     database,
		handler:      handler,
		storageClass: storageClass,
		repoID:       repoID,
		orgID:        session.OrgID,
		orgUUID:      orgUUID,
		userID:       session.UserID,
		sessionID:    sessionID,
		blockID:      blockID,
		sha1ID:       externalSHA1,
		content:      content,
		filename:     "sessionupload-head-" + uuid.NewString()[:8] + ".txt",
		target:       x1Target(storageClass, storageKey),
	}
	inner.headBefore = borrowedFSReadHead(t, database, session.OrgID, repoID)
	if inner.headBefore == "" {
		t.Fatal("library has empty HEAD before commit")
	}
	return &sessionUploadHeadFixture{borrowedFSHeadFixture: inner, sessionRef: sessionRef}
}

// dropOwnSessionRef removes the fixture's own up:<session> reference,
// modeling the writer's ONLY pre-existing liveness having lapsed (e.g. TTL
// expiry) before GC's zero-proof read, so a subsequent GC claim can prove
// zero references without racing this fixture's real reference.
func (fx *sessionUploadHeadFixture) dropOwnSessionRef(t *testing.T) {
	t.Helper()
	if err := fx.database.RemoveBlockReference(fx.orgID, fx.blockID, fx.sessionRef); err != nil {
		t.Fatalf("drop own session ref: %v", err)
	}
}
