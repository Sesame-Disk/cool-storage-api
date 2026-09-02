//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v2pkg "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TestBorrowedFSOwnLiveness drives in-process CreateFileFromBlocks through the
// production BorrowedFS own-liveness and final-fence path with real Cassandra
// and MinIO. The publication barriers only pause the request at deterministic
// points; they do not implement the protocol.
func TestBorrowedFSOwnLiveness(t *testing.T) {
	requireCassandra(t)
	gate := borrowedFSRequireOwnLivenessEvidence(t)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	storageClass := x1StorageClass(t)
	handler := newBorrowedFSHeadHandler(t, database, storageClass)
	gin.SetMode(gin.TestMode)

	t.Run("borrowedExactOwnPin", func(t *testing.T) {
		fx := newBorrowedFSHeadFixture(t, database, handler, storageClass)
		borrowedFSInstallBarriers(t, fx,
			func() {},
			func() { fx.assertOwnPinVisible(t, store, "borrowedExactOwnPin") },
			func() {},
			func() error { return nil },
		)
		rec := fx.commit(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("borrowedExactOwnPin: commit status=%d body=%s", rec.Code, rec.Body.String())
		}
		fx.assertHeadAdvanced(t)
		if !x1HasFSReferrer(t, database, fx.orgUUID, fx.blockID) {
			t.Fatal("borrowedExactOwnPin: expected fs: after promote")
		}
		borrowedFSOwnLivenessEvidence.borrowedExactOwnPin = true
	})

	t.Run("sessionUploadNoExtraPin", func(t *testing.T) {
		fx := newBorrowedFSHeadFixture(t, database, handler, storageClass)
		fx.pinSessionUpload(t)
		before := borrowedFSCountPrefix(t, database, fx.orgID, fx.blockID, "up:")
		borrowedFSInstallBarriers(t, fx,
			func() {},
			func() {
				after := borrowedFSCountPrefix(t, database, fx.orgID, fx.blockID, "up:")
				if after != before {
					t.Fatalf("sessionUploadNoExtraPin: up: count changed from %d to %d", before, after)
				}
			},
			func() {},
			func() error { return nil },
		)
		rec := fx.commit(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("sessionUploadNoExtraPin: commit status=%d body=%s", rec.Code, rec.Body.String())
		}
		borrowedFSOwnLivenessEvidence.sessionUploadNoExtraPin = true
	})

	t.Run("livenessFailureNoPublication", func(t *testing.T) {
		fx := newBorrowedFSHeadFixture(t, database, handler, storageClass)
		restore := v2pkg.SetFileFromBlocksOwnLivenessFailureForTest(fmt.Errorf("injected liveness failure"))
		t.Cleanup(restore)
		rec := fx.commit(t)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("livenessFailureNoPublication: commit status=%d body=%s", rec.Code, rec.Body.String())
		}
		fx.assertHeadUnchanged(t)
		if fx.hasOwnFSReferrer(t) {
			t.Fatal("livenessFailureNoPublication: unexpected fs: after own-liveness failure")
		}
		borrowedFSOwnLivenessEvidence.livenessFailureNoPublication = true
	})

	t.Run("writerFirst", func(t *testing.T) {
		fx := newBorrowedFSHeadFixture(t, database, handler, storageClass)
		borrowedFSInstallBarriers(t, fx,
			func() {},
			func() {
				fx.dropForeignFS(t)
				attempt := x1Attempt(fx.target, "writer-first")
				x1ClaimAcquired(t, store, fx.orgUUID, fx.blockID, attempt)
				hasRefs, err := store.BlockHasReferencesGlobal(fx.orgUUID, fx.blockID)
				if err != nil || !hasRefs {
					t.Fatalf("writerFirst: EACH_QUORUM missed up:<session> visible=%v err=%v", hasRefs, err)
				}
				released, err := store.ReleaseBlockClaim(fx.orgUUID, fx.blockID, attempt)
				if err != nil || released != gcpkg.BlockReleaseReleased {
					t.Fatalf("writerFirst: release = %s, %v", released, err)
				}
			},
			func() {},
			func() error {
				fx.assertOwnPinVisible(t, store, "writerFirst: pin must remain visible at beforeHead")
				fenced, err := database.BlockDeleteFenceActive(fx.orgID, fx.blockID)
				if err != nil {
					t.Fatalf("writerFirst: BlockDeleteFenceActive: %v", err)
				}
				if fenced {
					t.Fatal("writerFirst: expected no active fence before HEAD")
				}
				return nil
			},
		)
		rec := fx.commit(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("writerFirst: commit status=%d body=%s", rec.Code, rec.Body.String())
		}
		fx.assertHeadAdvanced(t)
		if !x1HasFSReferrer(t, database, fx.orgUUID, fx.blockID) {
			t.Fatal("writerFirst: expected fs: after promote")
		}
		borrowedFSOwnLivenessEvidence.writerFirst = true
	})

	t.Run("gcFirst", func(t *testing.T) {
		fx := newBorrowedFSHeadFixture(t, database, handler, storageClass)
		var attempt gcpkg.BlockDeleteAuthority
		borrowedFSInstallBarriers(t, fx,
			func() {
				fx.dropForeignFS(t)
				attempt = x1CommitHandoffAfterZeroRefs(t, store, fx.orgUUID, fx.blockID, x1Attempt(fx.target, "gc-first"))
			},
			func() { fx.assertOwnPinVisible(t, store, "gcFirst: late own pin must land") },
			func() { fx.assertPubCount(t, 1, "gcFirst: pub: must be staged before HEAD") },
			func() error {
				fenced, err := database.BlockDeleteFenceActive(fx.orgID, fx.blockID)
				if err != nil {
					t.Fatalf("gcFirst: BlockDeleteFenceActive: %v", err)
				}
				if !fenced {
					t.Fatal("gcFirst: expected active fence before HEAD")
				}
				return nil
			},
		)
		rec := fx.commit(t)
		if rec.Code != http.StatusConflict {
			t.Fatalf("gcFirst: commit status=%d body=%s; want 409", rec.Code, rec.Body.String())
		}
		fx.assertHeadUnchanged(t)
		fx.assertDUnrevoked(t, attempt)
		if fx.hasOwnFSReferrer(t) {
			t.Fatal("gcFirst: unexpected fs: after fenced publication")
		}
		fx.assertPubCount(t, 0, "gcFirst: fence abort must drop staged pub:")
		borrowedFSOwnLivenessEvidence.gcFirst = true
	})

	t.Run("lateOwnPinAfterZeroProof", func(t *testing.T) {
		fx := newBorrowedFSHeadFixture(t, database, handler, storageClass)
		var attempt gcpkg.BlockDeleteAuthority
		borrowedFSInstallBarriers(t, fx,
			func() {
				fx.dropForeignFS(t)
				attempt = x1Attempt(fx.target, "late-own-pin")
				x1ClaimAcquired(t, store, fx.orgUUID, fx.blockID, attempt)
				hasRefs, err := store.BlockHasReferencesGlobal(fx.orgUUID, fx.blockID)
				if err != nil || hasRefs {
					t.Fatalf("lateOwnPinAfterZeroProof: zero-proof = %v %v", hasRefs, err)
				}
			},
			func() {
				fx.assertOwnPinVisible(t, store, "lateOwnPinAfterZeroProof: late up:<session> must land")
				handoff, err := store.CommitBlockDeleteOrphanHandoff(fx.orgUUID, fx.blockID, attempt)
				if err != nil || (handoff.Outcome != gcpkg.BlockDeleteHandoffCommitted && handoff.Outcome != gcpkg.BlockDeleteHandoffAlreadyCommitted) {
					t.Fatalf("lateOwnPinAfterZeroProof: handoff after late pin = %s %v", handoff.Outcome, err)
				}
			},
			func() { fx.assertPubCount(t, 1, "lateOwnPinAfterZeroProof: pub: must be staged before HEAD") },
			func() error {
				fenced, err := database.BlockDeleteFenceActive(fx.orgID, fx.blockID)
				if err != nil {
					t.Fatalf("lateOwnPinAfterZeroProof: BlockDeleteFenceActive: %v", err)
				}
				if !fenced {
					t.Fatal("lateOwnPinAfterZeroProof: expected active fence despite late own pin")
				}
				return nil
			},
		)
		rec := fx.commit(t)
		if rec.Code != http.StatusConflict {
			t.Fatalf("lateOwnPinAfterZeroProof: commit status=%d body=%s; want 409", rec.Code, rec.Body.String())
		}
		fx.assertHeadUnchanged(t)
		fx.assertDUnrevoked(t, attempt)
		if fx.hasOwnFSReferrer(t) {
			t.Fatal("lateOwnPinAfterZeroProof: unexpected fs: after fenced publication")
		}
		fx.assertPubCount(t, 0, "lateOwnPinAfterZeroProof: fence abort must drop staged pub:")
		borrowedFSOwnLivenessEvidence.lateOwnPinAfterZeroProof = true
	})

	t.Run("upPubDedup", func(t *testing.T) {
		fx := newBorrowedFSHeadFixture(t, database, handler, storageClass)
		borrowedFSInstallBarriers(t, fx,
			func() {},
			func() {},
			func() {
				if borrowedFSCountPrefix(t, database, fx.orgID, fx.blockID, "up:") != 1 || borrowedFSCountPrefix(t, database, fx.orgID, fx.blockID, "pub:") != 1 {
					t.Fatalf("upPubDedup: expected exactly one up: and one pub: before HEAD")
				}
			},
			func() error { return nil },
		)
		rec := fx.commit(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("upPubDedup: commit status=%d body=%s; want 200", rec.Code, rec.Body.String())
		}
		retry := fx.commit(t)
		if retry.Code != http.StatusOK {
			t.Fatalf("upPubDedup: retry status=%d body=%s", retry.Code, retry.Body.String())
		}
		if borrowedFSCountPrefix(t, database, fx.orgID, fx.blockID, "up:") != 1 || !fx.hasOwnFSReferrer(t) || borrowedFSCountPrefix(t, database, fx.orgID, fx.blockID, "fs:"+fx.repoID+":") != 1 {
			t.Fatalf("upPubDedup: retry duplicated liveness references")
		}
		borrowedFSOwnLivenessEvidence.upPubDedup = true
	})

	gate.observed = borrowedFSOwnLivenessEvidence.complete()
	t.Logf("BORROWEDFS_OWN_LIVENESS_EVIDENCE missing=%v complete=%t", borrowedFSOwnLivenessEvidence.missing(), borrowedFSOwnLivenessEvidence.complete())
}

type borrowedFSHeadFixture struct {
	database     *dbpkg.DB
	handler      *v2pkg.FileHandler
	storageClass string
	repoID       string
	orgID        string
	orgUUID      uuid.UUID
	userID       string
	sessionID    string
	blockID      string
	content      []byte
	foreignFS    string
	filename     string
	headBefore   string
	target       gcpkg.BlockDeleteTarget
}

func newBorrowedFSHeadHandler(t *testing.T, database *dbpkg.DB, storageClass string) *v2pkg.FileHandler {
	t.Helper()
	s3Store := newVerificationS3Store(t)
	manager := storage.NewManager()
	manager.SetDefaultClass(storageClass)
	manager.RegisterBackend(storageClass, s3Store, "")
	cfg := &config.Config{
		WebUploads: config.WebUploadsConfig{EnableWebBlockUpload: true},
		Storage:    config.StorageConfig{DefaultClass: storageClass},
	}
	return v2pkg.NewFileHandler(database, cfg, s3Store, manager, nil, "", nil)
}

func newBorrowedFSHeadFixture(t *testing.T, database *dbpkg.DB, handler *v2pkg.FileHandler, storageClass string) *borrowedFSHeadFixture {
	t.Helper()
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-bfsh-%d", time.Now().UnixNano()))
	content := []byte("borrowedfs-head-" + uuid.NewString())
	sessionID := webCreateBlockSession(t, adminClient, repoID, "/", int64(len(content)))
	session, ok, err := database.GetBlockUploadSession(sessionID)
	if err != nil || !ok {
		t.Fatalf("read block-upload session %s: ok=%v err=%v", sessionID, ok, err)
	}
	orgUUID, err := uuid.Parse(session.OrgID)
	if err != nil {
		t.Fatalf("parse session org_id %q: %v", session.OrgID, err)
	}
	blockID, sha1ID, storageKey := borrowedFSSeedPhysical(t, database, orgUUID, content, storageClass)
	foreignLibrary := uuid.NewString()
	foreignFS := dbpkg.BlockReferrerForFSObject(foreignLibrary, uuid.NewString())
	sessionRef := dbpkg.BlockReferrerForUpload(sessionID)
	x1Cleanup(t, database, orgUUID, blockID, foreignFS, sessionRef)
	if err := database.AddBlockReference(session.OrgID, blockID, foreignFS, foreignLibrary, 0); err != nil {
		t.Fatalf("add foreign fs: %v", err)
	}
	fx := &borrowedFSHeadFixture{
		database:     database,
		handler:      handler,
		storageClass: storageClass,
		repoID:       repoID,
		orgID:        session.OrgID,
		orgUUID:      orgUUID,
		userID:       session.UserID,
		sessionID:    sessionID,
		blockID:      blockID,
		content:      content,
		foreignFS:    foreignFS,
		filename:     "borrowedfs-head-" + uuid.NewString()[:8] + ".txt",
		target:       x1Target(storageClass, storageKey),
	}
	fx.headBefore = borrowedFSReadHead(t, database, session.OrgID, repoID)
	if fx.headBefore == "" {
		t.Fatal("library has empty HEAD before commit")
	}
	t.Cleanup(func() {
		_ = database.Session().Query(
			`DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`,
			session.OrgID, dbpkg.PlainBlockRepresentationID, sha1ID,
		).Exec()
	})
	return fx
}

func borrowedFSSeedPhysical(t *testing.T, database *dbpkg.DB, orgID uuid.UUID, content []byte, storageClass string) (blockID, sha1ID, storageKey string) {
	t.Helper()
	digest := sha256.Sum256(content)
	blockID = hex.EncodeToString(digest[:])
	sum := sha1.Sum(content)
	sha1ID = hex.EncodeToString(sum[:])
	blockStore := newVerificationBlockStore(t, orgID.String())
	key, err := blockStore.MintStorageKey(blockID)
	if err != nil {
		t.Fatalf("MintStorageKey: %v", err)
	}
	if _, err := blockStore.PutObjectAutoDirect(t.Context(), key, content); err != nil {
		t.Fatalf("seed PUT %s: %v", key, err)
	}
	installed := database.InstallBlockMetadata(t.Context(), orgID.String(), dbpkg.PlainBlockRepresentationID, blockID, sha1ID, len(content), dbpkg.BlockPhysicalLocation{
		StorageClass: storageClass,
		StorageKey:   key,
	})
	if installed.Outcome != dbpkg.InstallBlockMetadataApplied {
		t.Fatalf("InstallBlockMetadata: outcome=%v cause=%v", installed.Outcome, installed.Cause)
	}
	if err := database.WriteBlockIDMapping(orgID.String(), dbpkg.PlainBlockRepresentationID, sha1ID, blockID, time.Now().UTC()); err != nil {
		t.Fatalf("WriteBlockIDMapping: %v", err)
	}
	t.Cleanup(func() {
		_ = blockStore.DeleteBlockByStorageKey(context.Background(), key)
	})
	return blockID, sha1ID, key
}

func borrowedFSInstallBarriers(t *testing.T, fx *borrowedFSHeadFixture, afterVerified, afterBorrowedPin, afterStaged func(), beforeHead func() error) {
	t.Helper()
	t.Cleanup(v2pkg.SetFileFromBlocksPublicationBarriersForTest(fx.repoID, afterVerified, afterBorrowedPin, afterStaged, beforeHead))
}

func (fx *borrowedFSHeadFixture) dropForeignFS(t *testing.T) {
	t.Helper()
	if err := fx.database.RemoveBlockReference(fx.orgID, fx.blockID, fx.foreignFS); err != nil {
		t.Fatalf("drop foreign fs: %v", err)
	}
}

func (fx *borrowedFSHeadFixture) pinSessionUpload(t *testing.T) {
	t.Helper()
	referrer := dbpkg.BlockReferrerForUpload(fx.sessionID)
	expiresAt := time.Now().UTC().Add(time.Duration(dbpkg.ProvisionalBlockReferenceTTLSeconds) * time.Second)
	if err := fx.database.AddProvisionalBlockReferenceWithExpiry(
		fx.orgID, fx.blockID, referrer, fx.repoID, fx.storageClass, expiresAt,
	); err != nil {
		t.Fatalf("pin up:<session>: %v", err)
	}
}

func (fx *borrowedFSHeadFixture) assertOwnPinVisible(t *testing.T, store *gcpkg.CassandraStore, msg string) {
	t.Helper()
	hasRefs, err := store.BlockHasReferencesGlobal(fx.orgUUID, fx.blockID)
	if err != nil || !hasRefs {
		t.Fatalf("%s: BlockHasReferencesGlobal visible=%v err=%v", msg, hasRefs, err)
	}
	exists, err := store.BlockReferenceExists(fx.orgUUID, fx.blockID, dbpkg.BlockReferrerForUpload(fx.sessionID))
	if err != nil || !exists {
		t.Fatalf("%s: up:<session> exists=%v err=%v", msg, exists, err)
	}
}

func (fx *borrowedFSHeadFixture) commit(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"session":    fx.sessionID,
		"parent_dir": "/",
		"filename":   fx.filename,
		"size":       len(fx.content),
		"blocks": []map[string]interface{}{
			{"sha256": fx.blockID, "size": len(fx.content)},
		},
	})
	if err != nil {
		t.Fatalf("marshal file-from-blocks body: %v", err)
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/repos/"+fx.repoID+"/file-from-blocks/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Params = gin.Params{{Key: "repo_id", Value: fx.repoID}}
	c.Set("org_id", fx.orgID)
	c.Set("user_id", fx.userID)
	fx.handler.CreateFileFromBlocks(c)
	return rec
}

func (fx *borrowedFSHeadFixture) assertHeadAdvanced(t *testing.T) {
	t.Helper()
	head := borrowedFSReadHead(t, fx.database, fx.orgID, fx.repoID)
	if head == "" || head == fx.headBefore {
		t.Fatalf("HEAD did not advance: before=%s after=%s", fx.headBefore, head)
	}
}

func (fx *borrowedFSHeadFixture) assertHeadUnchanged(t *testing.T) {
	t.Helper()
	head := borrowedFSReadHead(t, fx.database, fx.orgID, fx.repoID)
	if head != fx.headBefore {
		t.Fatalf("HEAD must not occur: before=%s after=%s", fx.headBefore, head)
	}
}

func (fx *borrowedFSHeadFixture) hasOwnFSReferrer(t *testing.T) bool {
	t.Helper()
	return borrowedFSCountPrefix(t, fx.database, fx.orgID, fx.blockID, "fs:"+fx.repoID+":") > 0
}

func (fx *borrowedFSHeadFixture) assertPubCount(t *testing.T, want int, msg string) {
	t.Helper()
	got := borrowedFSCountPrefix(t, fx.database, fx.orgID, fx.blockID, "pub:")
	if got != want {
		t.Fatalf("%s: pub: count=%d, want %d", msg, got, want)
	}
}

func (fx *borrowedFSHeadFixture) assertDUnrevoked(t *testing.T, attempt gcpkg.BlockDeleteAuthority) {
	t.Helper()
	state, claimID, handoff, _, _ := x1ReadCommittedRow(t, fx.database, fx.orgUUID, fx.blockID)
	if state != "deleting" || !handoff || claimID != attempt.ClaimID {
		t.Fatalf("D revoked: state=%s handoff=%v claim=%s want deleting/true/%s", state, handoff, claimID, attempt.ClaimID)
	}
}

func borrowedFSReadHead(t *testing.T, database *dbpkg.DB, orgID, repoID string) string {
	t.Helper()
	var head string
	if err := database.Session().Query(
		`SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID, repoID,
	).Scan(&head); err != nil {
		t.Fatalf("read library HEAD: %v", err)
	}
	return head
}

func borrowedFSCountPrefix(t *testing.T, database *dbpkg.DB, orgID, blockID, prefix string) int {
	t.Helper()
	referrers, err := database.ListBlockReferrers(orgID, blockID)
	if err != nil {
		t.Fatalf("ListBlockReferrers: %v", err)
	}
	count := 0
	for _, referrer := range referrers {
		if strings.HasPrefix(referrer, prefix) {
			count++
		}
	}
	return count
}

func borrowedFSHasPrefix(t *testing.T, database *dbpkg.DB, orgID, blockID, prefix string) bool {
	return borrowedFSCountPrefix(t, database, orgID, blockID, prefix) > 0
}
