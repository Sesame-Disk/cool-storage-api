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

// TestBorrowedFSHeadCharacterization drives in-process CreateFileFromBlocks
// through real HEAD+promote. Current legs measure today's unguarded publication
// after a BorrowedFS cut. Harness legs inject a test-only own pin and a
// beforeHead fence; they are not production protocol.
func TestBorrowedFSHeadCharacterization(t *testing.T) {
	requireCassandra(t)
	gate := borrowedFSRequireHeadEvidence(t)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	storageClass := x1StorageClass(t)
	handler := newBorrowedFSHeadHandler(t, database, storageClass)
	gin.SetMode(gin.TestMode)

	t.Run("currentHeadAfterCut", func(t *testing.T) {
		fx := newBorrowedFSHeadFixture(t, database, handler, storageClass)
		var attempt gcpkg.BlockDeleteAuthority
		var sawPub bool
		borrowedFSInstallBarriers(t,
			func() {
				fx.dropForeignFS(t)
				attempt = x1CommitHandoffAfterZeroRefs(t, store, fx.orgUUID, fx.blockID, x1Attempt(fx.target, "head-after-cut"))
			},
			func() {
				if !borrowedFSHasPrefix(t, database, fx.orgID, fx.blockID, "pub:") {
					t.Fatal("currentHeadAfterCut: expected pub: after stage")
				}
				sawPub = true
			},
			func() error { return nil },
		)
		rec := fx.commit(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("currentHeadAfterCut: commit status=%d body=%s", rec.Code, rec.Body.String())
		}
		if !sawPub {
			t.Fatal("currentHeadAfterCut: afterStaged did not observe pub:")
		}
		fx.assertHeadAdvanced(t)
		if !x1HasFSReferrer(t, database, fx.orgUUID, fx.blockID) {
			t.Fatal("currentHeadAfterCut: expected fs: after promote")
		}
		fx.assertDUnrevoked(t, attempt)
		fx.assertNewRequestBlockedByGC(t)
		t.Log("currentHeadAfterCut UNGUARDED: HEAD+promote landed after the cut; D unrevoked; new-request BlockedByGC. This is the last dangerous point on the current BorrowedFS path.")
		borrowedFSHeadEvidence.currentHeadAfterCut = true
	})

	t.Run("currentPubRevokesZeroProof", func(t *testing.T) {
		fx := newBorrowedFSHeadFixture(t, database, handler, storageClass)
		borrowedFSInstallBarriers(t,
			func() { fx.dropForeignFS(t) },
			func() {
				if !borrowedFSHasPrefix(t, database, fx.orgID, fx.blockID, "pub:") {
					t.Fatal("currentPubRevokesZeroProof: expected pub: after stage")
				}
				attempt := x1Attempt(fx.target, "pub-revokes")
				x1ClaimAcquired(t, store, fx.orgUUID, fx.blockID, attempt)
				hasRefs, err := store.BlockHasReferencesGlobal(fx.orgUUID, fx.blockID)
				if err != nil || !hasRefs {
					t.Fatalf("currentPubRevokesZeroProof: EACH_QUORUM missed pub: visible=%v err=%v", hasRefs, err)
				}
				released, err := store.ReleaseBlockClaim(fx.orgUUID, fx.blockID, attempt)
				if err != nil || released != gcpkg.BlockReleaseReleased {
					t.Fatalf("currentPubRevokesZeroProof: release = %s, %v", released, err)
				}
			},
			func() error { return nil },
		)
		rec := fx.commit(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("currentPubRevokesZeroProof: commit status=%d body=%s", rec.Code, rec.Body.String())
		}
		fx.assertHeadAdvanced(t)
		t.Log("currentPubRevokesZeroProof: pub: is EACH_QUORUM-visible and revokes a post-stage zero-proof; HEAD proceeds")
		borrowedFSHeadEvidence.currentPubRevokesZeroProof = true
	})

	t.Run("currentPubAfterZeroProof", func(t *testing.T) {
		fx := newBorrowedFSHeadFixture(t, database, handler, storageClass)
		var attempt gcpkg.BlockDeleteAuthority
		borrowedFSInstallBarriers(t,
			func() {
				fx.dropForeignFS(t)
				attempt = x1Attempt(fx.target, "pub-after-zero")
				x1ClaimAcquired(t, store, fx.orgUUID, fx.blockID, attempt)
				hasRefs, err := store.BlockHasReferencesGlobal(fx.orgUUID, fx.blockID)
				if err != nil || hasRefs {
					t.Fatalf("currentPubAfterZeroProof: zero-proof = %v %v", hasRefs, err)
				}
			},
			func() {
				if !borrowedFSHasPrefix(t, database, fx.orgID, fx.blockID, "pub:") {
					t.Fatal("currentPubAfterZeroProof: expected pub: after zero-proof")
				}
				handoff, err := store.CommitBlockDeleteOrphanHandoff(fx.orgUUID, fx.blockID, attempt)
				if err != nil || (handoff.Outcome != gcpkg.BlockDeleteHandoffCommitted && handoff.Outcome != gcpkg.BlockDeleteHandoffAlreadyCommitted) {
					t.Fatalf("currentPubAfterZeroProof: handoff after late pub: = %s %v", handoff.Outcome, err)
				}
			},
			func() error { return nil },
		)
		rec := fx.commit(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("currentPubAfterZeroProof: commit status=%d body=%s", rec.Code, rec.Body.String())
		}
		fx.assertHeadAdvanced(t)
		fx.assertDUnrevoked(t, attempt)
		fx.assertNewRequestBlockedByGC(t)
		t.Log("currentPubAfterZeroProof C2 analog: pub: after zero-proof does not revoke D; handoff still commits; current HEAD still proceeds")
		borrowedFSHeadEvidence.currentPubAfterZeroProof = true
	})

	t.Run("harnessWriterWins", func(t *testing.T) {
		fx := newBorrowedFSHeadFixture(t, database, handler, storageClass)
		borrowedFSInstallBarriers(t,
			func() {
				fx.pinSessionUpload(t)
				fx.dropForeignFS(t)
				attempt := x1Attempt(fx.target, "harness-writer")
				x1ClaimAcquired(t, store, fx.orgUUID, fx.blockID, attempt)
				hasRefs, err := store.BlockHasReferencesGlobal(fx.orgUUID, fx.blockID)
				if err != nil || !hasRefs {
					t.Fatalf("harnessWriterWins: EACH_QUORUM missed up:<session> visible=%v err=%v", hasRefs, err)
				}
				released, err := store.ReleaseBlockClaim(fx.orgUUID, fx.blockID, attempt)
				if err != nil || released != gcpkg.BlockReleaseReleased {
					t.Fatalf("harnessWriterWins: release = %s, %v", released, err)
				}
			},
			func() {},
			func() error { return nil },
		)
		rec := fx.commit(t)
		if rec.Code != http.StatusOK {
			t.Fatalf("harnessWriterWins: commit status=%d body=%s", rec.Code, rec.Body.String())
		}
		fx.assertHeadAdvanced(t)
		if !x1HasFSReferrer(t, database, fx.orgUUID, fx.blockID) {
			t.Fatal("harnessWriterWins: expected fs: after promote")
		}
		t.Log("harnessWriterWins: own up:<session> pin before zero-proof revoked the authorizing read; HEAD+fs: landed. Harness is not production protocol.")
		borrowedFSHeadEvidence.harnessWriterWins = true
	})

	t.Run("harnessCutAfterClassify", func(t *testing.T) {
		fx := newBorrowedFSHeadFixture(t, database, handler, storageClass)
		var attempt gcpkg.BlockDeleteAuthority
		var headCalled bool
		borrowedFSInstallBarriers(t,
			func() {
				fx.dropForeignFS(t)
				attempt = x1CommitHandoffAfterZeroRefs(t, store, fx.orgUUID, fx.blockID, x1Attempt(fx.target, "harness-cut"))
			},
			func() {},
			func() error {
				fenced, err := database.BlockDeleteFenceActive(fx.orgID, fx.blockID)
				if err != nil {
					t.Fatalf("harnessCutAfterClassify: BlockDeleteFenceActive: %v", err)
				}
				if !fenced {
					t.Fatal("harnessCutAfterClassify: expected active fence before HEAD")
				}
				headCalled = true
				return v2pkg.ErrBlockDeleteInProgress
			},
		)
		rec := fx.commit(t)
		if rec.Code != http.StatusConflict {
			t.Fatalf("harnessCutAfterClassify: commit status=%d body=%s; want 409", rec.Code, rec.Body.String())
		}
		if !headCalled {
			t.Fatal("harnessCutAfterClassify: beforeHead fence was not reached")
		}
		fx.assertHeadUnchanged(t)
		fx.assertDUnrevoked(t, attempt)
		t.Log("harnessCutAfterClassify: D2 interleaving then beforeHead fence aborted HEAD. Harness is not production protocol.")
		borrowedFSHeadEvidence.harnessCutAfterClassify = true
	})

	t.Run("harnessLatePubStillFenced", func(t *testing.T) {
		fx := newBorrowedFSHeadFixture(t, database, handler, storageClass)
		var attempt gcpkg.BlockDeleteAuthority
		var sawPub bool
		borrowedFSInstallBarriers(t,
			func() {
				fx.dropForeignFS(t)
				attempt = x1CommitHandoffAfterZeroRefs(t, store, fx.orgUUID, fx.blockID, x1Attempt(fx.target, "harness-late-pub"))
			},
			func() {
				if !borrowedFSHasPrefix(t, database, fx.orgID, fx.blockID, "pub:") {
					t.Fatal("harnessLatePubStillFenced: post-cut pub: must still land")
				}
				sawPub = true
			},
			func() error {
				fenced, err := database.BlockDeleteFenceActive(fx.orgID, fx.blockID)
				if err != nil {
					t.Fatalf("harnessLatePubStillFenced: BlockDeleteFenceActive: %v", err)
				}
				if !fenced {
					t.Fatal("harnessLatePubStillFenced: expected active fence despite pub:")
				}
				return v2pkg.ErrBlockDeleteInProgress
			},
		)
		rec := fx.commit(t)
		if rec.Code != http.StatusConflict {
			t.Fatalf("harnessLatePubStillFenced: commit status=%d body=%s; want 409", rec.Code, rec.Body.String())
		}
		if !sawPub {
			t.Fatal("harnessLatePubStillFenced: afterStaged did not observe post-cut pub:")
		}
		fx.assertHeadUnchanged(t)
		fx.assertDUnrevoked(t, attempt)
		t.Log("harnessLatePubStillFenced: post-cut pub: landed; fence still aborted HEAD. Harness is not production protocol.")
		borrowedFSHeadEvidence.harnessLatePubStillFenced = true
	})

	gate.observed = borrowedFSHeadEvidence.complete()
	t.Logf("BORROWEDFS_HEAD_CHARACTERIZATION_EVIDENCE missing=%v complete=%t", borrowedFSHeadEvidence.missing(), borrowedFSHeadEvidence.complete())
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

func borrowedFSInstallBarriers(t *testing.T, afterVerified, afterStaged func(), beforeHead func() error) {
	t.Helper()
	t.Cleanup(v2pkg.SetFileFromBlocksPublicationBarriersForTest(afterVerified, afterStaged, beforeHead))
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

func (fx *borrowedFSHeadFixture) assertDUnrevoked(t *testing.T, attempt gcpkg.BlockDeleteAuthority) {
	t.Helper()
	state, claimID, handoff, _, _ := x1ReadCommittedRow(t, fx.database, fx.orgUUID, fx.blockID)
	if state != "deleting" || !handoff || claimID != attempt.ClaimID {
		t.Fatalf("D revoked: state=%s handoff=%v claim=%s want deleting/true/%s", state, handoff, claimID, attempt.ClaimID)
	}
}

func (fx *borrowedFSHeadFixture) assertNewRequestBlockedByGC(t *testing.T) {
	t.Helper()
	probe, err := fx.database.ProbeBlockReuse(fx.orgID, fx.blockID)
	if err != nil || probe.Decision != dbpkg.BlockReuseBlockedByGC {
		t.Fatalf("new-request probe = %+v %v; want BlockedByGC", probe, err)
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

func borrowedFSHasPrefix(t *testing.T, database *dbpkg.DB, orgID, blockID, prefix string) bool {
	t.Helper()
	referrers, err := database.ListBlockReferrers(orgID, blockID)
	if err != nil {
		t.Fatalf("ListBlockReferrers: %v", err)
	}
	for _, referrer := range referrers {
		if strings.HasPrefix(referrer, prefix) {
			return true
		}
	}
	return false
}
