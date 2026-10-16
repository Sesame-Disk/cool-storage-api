//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	apipkg "github.com/Sesame-Disk/sesamefs/internal/api"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TestE1DelayedPutCannotRestoreRetiredPhysicalLife exercises the actual Sync
// PutBlock handler against Cassandra and the SILO MinIO-compatible S3 backend. It pauses after the production
// repair-authority check has authorized exact P1/K1, lets the real G5 worker
// commit and fully retire D1, then releases the real object PUT to K1.
func TestE1DelayedPutCannotRestoreRetiredPhysicalLife(t *testing.T) {
	e1DelayedPut(t, false)
}

// The combined D0 H witness: P2 is protected before D1 deletes K1, and the
// delayed writer subsequently attempts real metadata/HEAD publication.
func TestE1DelayedPutWithProtectedSuccessor(t *testing.T) {
	if endpoint := os.Getenv("SESAMEFS_E19_ISOLATED_URL"); endpoint != "" && os.Getenv("SESAMEFS_E1_H1_CHILD") != "1" {
		e1RunProtectedSuccessorIsolated(t, endpoint)
		return
	}
	e19RequireNoBackgroundGC(t)
	e1DelayedPut(t, true)
}

func e1DelayedPut(t *testing.T, protectedSuccessor bool) {
	t.Helper()
	requireCassandra(t)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	tenant := provisionIsolatedTenant(t, "e1-delayed-put")
	repoID := createTestLibrary(t, tenant.client, fmt.Sprintf("inttest-e1-delayed-put-%d", time.Now().UnixNano()))
	orgID := uuid.MustParse(tenant.orgID)
	class := x1StorageClass(t)

	manager := storage.NewManager()
	manager.SetDefaultClass(class)
	s3 := newVerificationS3Store(t)
	manager.RegisterBackend(class, s3, "")
	handler := apipkg.NewSyncHandler(database, s3, manager, &config.Config{Storage: config.StorageConfig{DefaultClass: class}}, nil)
	blockStore := newVerificationBlockStore(t, orgID.String())

	invoke := func(content []byte, externalID string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPut, "/seafhttp/repo/"+repoID+"/block/"+externalID, bytes.NewReader(content))
		ctx.Params = gin.Params{{Key: "repo_id", Value: repoID}, {Key: "block_id", Value: externalID}}
		ctx.Set("org_id", tenant.orgID)
		ctx.Set("user_id", tenant.userID)
		handler.PutBlock(ctx)
		return recorder
	}

	// Positive control: the same real handler repairs a missing object while
	// P1 remains canonical, registers its provisional reference, and succeeds.
	t.Run("noGCControl", func(t *testing.T) {
		content := []byte("e1 delayed put no-gc control " + uuid.NewString())
		blockID, key := x1SeedPhysical(t, database, blockStore, orgID, content, class)
		x1Cleanup(t, database, orgID, blockID)
		e1CleanupSyncUpload(t, database, blockStore, tenant.orgID, repoID, blockID, sha1hex(content))
		if err := blockStore.DeleteBlockByStorageKey(t.Context(), key); err != nil {
			t.Fatalf("remove K1 for repair control: %v", err)
		}
		recorder := invoke(content, blockID)
		if recorder.Code != http.StatusOK {
			t.Fatalf("PutBlock without GC = %d %s, want 200", recorder.Code, recorder.Body.String())
		}
		x1AssertCanonicalPresent(t, store, orgID, blockID, key)
		if exists, err := blockStore.ObjectExists(t.Context(), key); err != nil || !exists {
			t.Fatalf("no-GC control did not restore K1: exists=%v err=%v", exists, err)
		}
		refs, err := database.ListBlockReferrers(orgID.String(), blockID)
		if err != nil || len(refs) == 0 {
			t.Fatalf("no-GC control did not register upload liveness: refs=%v err=%v", refs, err)
		}
	})

	var successor *e1ProtectedSuccessor
	t.Cleanup(func() {
		if successor != nil {
			successor.assertCleanup(t, database, store, blockStore)
		}
	})
	content := []byte("e1 delayed put after committed D " + uuid.NewString())
	blockID, key := x1SeedPhysical(t, database, blockStore, orgID, content, class)
	x1Cleanup(t, database, orgID, blockID)
	// Register after x1Cleanup: LIFO teardown reads and deletes the current K2
	// before its canonical row disappears, and removes the exact up:/expiry rows.
	e1CleanupSyncUpload(t, database, blockStore, tenant.orgID, repoID, blockID, sha1hex(content))
	sha1ID := sha1.Sum(content)
	externalID := hex.EncodeToString(sha1ID[:])
	representationID, err := dbpkg.ResolveBlockRepresentationID(database.Session(), orgID.String(), repoID)
	if err != nil {
		t.Fatalf("resolve library block representation: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Session().Query(`DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`, orgID.String(), representationID, externalID).Exec()
	})
	if err := blockStore.DeleteBlockByStorageKey(t.Context(), key); err != nil {
		t.Fatalf("remove K1 before delayed authorized repair: %v", err)
	}
	if exists, err := blockStore.ObjectExists(t.Context(), key); err != nil || exists {
		t.Fatalf("K1 must be absent before delayed repair: exists=%v err=%v", exists, err)
	}
	candidate := w2Candidate(t, store, orgID, blockID, class)

	putStarted := make(chan string, 1)
	releasePut := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releasePut) }) }
	defer release()
	var firstPut sync.Once
	restorePut := apipkg.SetSyncPutBlockAutoDirectForIntegration(func(ctx context.Context, targetStore *storage.BlockStore, storageKey string, data []byte) (string, error) {
		// Only the captured K1 is held; sync.Once alone would also block the
		// competing P2 writer while its callback is still running.
		if storageKey == key {
			firstPut.Do(func() {
				putStarted <- storageKey
				<-releasePut
			})
		}
		return targetStore.PutObjectAutoDirect(ctx, storageKey, data)
	})
	defer restorePut()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/seafhttp/repo/"+repoID+"/block/"+externalID, bytes.NewReader(content))
	ctx.Params = gin.Params{{Key: "repo_id", Value: repoID}, {Key: "block_id", Value: externalID}}
	ctx.Set("org_id", tenant.orgID)
	ctx.Set("user_id", tenant.userID)
	done := make(chan struct{})
	go func() {
		handler.PutBlock(ctx)
		close(done)
	}()
	// Join before restoring the global integration hook or cleaning up rows,
	// including when a new H1 boundary assertion fails while K1 is held.
	defer func() {
		release()
		select {
		case <-done:
		case <-time.After(45 * time.Second):
			t.Error("delayed PutBlock did not stop before hook restoration/teardown")
		}
	}()

	select {
	case authorizedKey := <-putStarted:
		if authorizedKey != key {
			t.Fatalf("authorized PUT target = %q, want exact P1 key %q", authorizedKey, key)
		}
	case <-time.After(20 * time.Second):
		release()
		<-done
		t.Fatal("real Sync PutBlock never reached the authorized physical PUT boundary")
	}

	// The hook is reached only after PutBlockMaterializationTarget has authorized
	// this exact persisted tuple. No request reference has been installed yet.
	if refs, err := database.ListBlockReferrers(orgID.String(), blockID); err != nil || len(refs) != 0 {
		release()
		<-done
		t.Fatalf("unexpected pre-PUT liveness refs=%v err=%v", refs, err)
	}
	worker := w2Worker(t, store, class)
	if processed, err := worker.ProcessOrgOnce(t.Context(), orgID); err != nil || processed != 1 {
		release()
		<-done
		t.Fatalf("real G5 worker did not process P1 candidate: processed=%d err=%v", processed, err)
	}
	// ProcessOrgOnce retires the canonical row before physical recovery.
	x1AssertCanonicalAbsent(t, store, orgID, blockID)
	if protectedSuccessor {
		successor = e1InstallProtectedSuccessor(t, database, store, blockStore, orgID, repoID, blockID, class, key, content, invoke)
	} else {
		w2AssertCommittedContinuation(t, store, orgID, blockID, class, key, blockStore)
	}
	if exists, err := blockStore.ObjectExists(t.Context(), key); err != nil || exists {
		release()
		<-done
		t.Fatalf("D1 did not retire K1 before delayed PUT: exists=%v err=%v", exists, err)
	}
	var claimID, phase, dClass, dKey string
	if err := database.Session().Query(`
		SELECT claim_id, phase, storage_class, storage_key FROM gc_block_delete_lifecycles
		WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).Scan(&claimID, &phase, &dClass, &dKey); err != nil {
		release()
		<-done
		t.Fatalf("read terminal D1 lifecycle: %v", err)
	}
	if claimID == "" || phase != gcpkg.BlockDeleteLifecyclePhaseTerminal || dClass != class || dKey != key {
		release()
		<-done
		t.Fatalf("D1 lifecycle does not identify retired P1: claim=%q phase=%q class=%q key=%q", claimID, phase, dClass, dKey)
	}
	if _, found, err := store.GetBlockGCCandidateExact(orgID, blockID, candidate.Identity()); err != nil || found {
		t.Fatalf("E1 delayed-PUT fixture still has an exact GC candidate after D1: found=%v err=%v", found, err)
	}

	release()
	select {
	case <-done:
	case <-time.After(45 * time.Second):
		t.Fatal("real Sync PutBlock did not finish after releasing delayed K1 write")
	}

	// K1 is expected to reappear as storage residue. That alone is not X1 RED.
	if exists, err := blockStore.ObjectExists(t.Context(), key); err != nil || !exists {
		t.Fatalf("delayed physical PUT did not leave expected K1 bytes: exists=%v err=%v", exists, err)
	}
	current, currentErr := store.GetBlockInfo(orgID, blockID)
	var referrers []string
	if currentErr == nil {
		if current.StorageClass == class && current.StorageKey == key {
			t.Fatalf("X1 RED: D1/P1 was reinstalled as the canonical life after delayed PUT: %+v", current)
		}
		if current.StorageKey == "" || current.StorageKey == key {
			t.Fatalf("post-D canonical life does not have a distinct physical identity: %+v", current)
		}
		currentBytes, err := blockStore.GetBlockByStorageKey(t.Context(), current.StorageKey)
		if err != nil || !bytes.Equal(currentBytes, content) {
			t.Fatalf("post-D canonical P2 does not contain uploaded bytes: err=%v", err)
		}
		referrers, err = database.ListBlockReferrers(orgID.String(), blockID)
		if err != nil {
			t.Fatalf("read durable referrers after delayed PUT: %v", err)
		}
		upRef, permanentRef := false, false
		for _, referrer := range referrers {
			upRef = upRef || strings.HasPrefix(referrer, "up:")
			permanentRef = permanentRef || strings.HasPrefix(referrer, "fs:")
		}
		if recorder.Code == http.StatusOK && (!upRef || permanentRef ||
			len(referrers) != 1 || !strings.HasPrefix(referrers[0], "up:")) {
			t.Fatalf("successful PutBlock must leave only logical upload liveness for P2, got %v", referrers)
		}
	} else if !errors.Is(currentErr, gocql.ErrNotFound) {
		t.Fatalf("post-D canonical read is neither an installed new life nor absent: %v", currentErr)
	}
	if recorder.Code != http.StatusOK && recorder.Code != http.StatusConflict {
		t.Fatalf("delayed PutBlock returned %d %s; expected success on a new life or fail-closed conflict", recorder.Code, recorder.Body.String())
	}
	if recorder.Code == http.StatusOK && currentErr != nil {
		t.Fatalf("PutBlock returned success without a current canonical block life: %v", currentErr)
	}
	if recorder.Code == http.StatusOK {
		var mappedID string
		if err := database.Session().Query(`SELECT internal_id FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`, orgID.String(), representationID, externalID).Scan(&mappedID); err != nil || mappedID != blockID {
			t.Fatalf("legacy mapping after successful new-life upload = %q, err=%v; want %s", mappedID, err, blockID)
		}
	}
	if successor != nil {
		successor.publishAndReplay(t, database, store, blockStore, handler, tenant.userID, content)
	}
	t.Logf("E1-PUT-01 result: D1 claim=%s terminal P1=(%s,%s); delayed PUT response=%d; K1_reappeared=true; canonical_after=%s; refs_after=%v; mapping_checked=%t", claimID, class, key, recorder.Code, func() string {
		if currentErr != nil {
			return "absent"
		}
		return fmt.Sprintf("(%s,%s)", current.StorageClass, current.StorageKey)
	}(), referrers, recorder.Code == http.StatusOK)
}

// Snapshot -> existing exact-id cleanup -> independent verification, all before
// x1Cleanup removes the canonical row. This covers both K1 and rematerialized K2.
func e1CleanupSyncUpload(t *testing.T, database *dbpkg.DB, bs *storage.BlockStore, org, repo, block, externalID string) {
	t.Helper()
	referrer := apipkg.SyncBlockUploadReferrerForIntegration(repo, block)
	var key string
	var expiresAt time.Time
	t.Cleanup(func() {
		if key != "" {
			if exists, err := bs.ObjectExists(context.Background(), key); err != nil || exists {
				t.Errorf("E1 teardown left physical object %s: exists=%v err=%v", key, exists, err)
			}
		}
		var remaining time.Time
		err := database.Session().Query(`SELECT expires_at FROM gc_provisional_block_refs WHERE org_id = ? AND block_id = ? AND referrer = ?`, org, block, referrer).Scan(&remaining)
		if !errors.Is(err, gocql.ErrNotFound) {
			t.Errorf("E1 teardown left provisional expiry: expires_at=%v err=%v", remaining, err)
		}
		if !expiresAt.IsZero() {
			var found string
			err := database.Session().Query(`SELECT referrer FROM gc_provisional_block_refs_by_day WHERE expiry_day = ? AND bucket = ? AND expires_at = ? AND org_id = ? AND block_id = ? AND referrer = ?`, dbpkg.GCProjectionUTCDate(expiresAt), dbpkg.GCDiscoveryBucket(org, block, referrer), expiresAt, org, block, referrer).Scan(&found)
			if !errors.Is(err, gocql.ErrNotFound) {
				t.Errorf("E1 teardown left expiry discovery: referrer=%q err=%v", found, err)
			}
		}
	})
	cleanupUploadedBlockArtifactsForTest(t, org, repo, block, externalID, referrer)
	t.Cleanup(func() {
		if err := database.Session().Query(`SELECT storage_key FROM blocks WHERE org_id = ? AND block_id = ?`, org, block).Scan(&key); err != nil && !errors.Is(err, gocql.ErrNotFound) {
			t.Errorf("snapshot E1 teardown key: %v", err)
		}
		if err := database.Session().Query(`SELECT expires_at FROM gc_provisional_block_refs WHERE org_id = ? AND block_id = ? AND referrer = ?`, org, block, referrer).Scan(&expiresAt); err != nil && !errors.Is(err, gocql.ErrNotFound) {
			t.Errorf("snapshot E1 teardown expiry: %v", err)
		}
	})
}
