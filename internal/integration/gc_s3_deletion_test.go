//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/google/uuid"
)

// These tests close the one gap the rest of the GC suite never covered: that a
// block deleted through the GC paths is *actually* gone from the object store
// (MinIO), not merely removed from Cassandra. Every other GC test asserts on
// Cassandra rows only; nothing ever queried the bucket.
//
// They verify physical objects through an independent BlockStore pointed at the
// same dev MinIO bucket. Tests that need deterministic draining construct the
// production worker adapters and call ProcessOrgOnce for only their fixture org;
// they never fan out across unrelated org queues.

func newVerificationS3Store(t *testing.T) *storage.S3Store {
	t.Helper()
	ctx := context.Background()
	s3cfg := storage.S3Config{
		Endpoint:        envOrDefault("S3_ENDPOINT", "http://minio:9000"),
		Bucket:          envOrDefault("S3_BUCKET", "sesamefs-blocks"),
		Region:          envOrDefault("S3_REGION", "us-east-1"),
		AccessKeyID:     envOrDefault("S3_ACCESS_KEY_ID", "minioadmin"),
		SecretAccessKey: envOrDefault("S3_SECRET_ACCESS_KEY", "minioadmin"),
		UsePathStyle:    true,
	}
	s3Store, err := storage.NewS3Store(ctx, s3cfg)
	if err != nil {
		t.Skipf("MinIO S3 store unavailable (%v); skipping", err)
	}
	if err := s3Store.HeadBucket(ctx); err != nil {
		t.Skipf("MinIO bucket %q unreachable (%v); skipping", s3cfg.Bucket, err)
	}
	return s3Store
}

// newVerificationBlockStore builds an org-scoped BlockStore pointed at the dev
// MinIO bucket for direct object existence checks.
func newVerificationBlockStore(t *testing.T, orgID string) *storage.BlockStore {
	t.Helper()
	blockStore, err := storage.NewOrgBlockStore(newVerificationS3Store(t), "blocks/", orgID)
	if err != nil {
		t.Fatalf("build org-scoped verification BlockStore: %v", err)
	}
	return blockStore
}

func TestGC_CrossOrgIdenticalBlockDeleteIsolation(t *testing.T) {
	requireCassandra(t)

	ctx := context.Background()
	content := []byte(fmt.Sprintf("gc-cross-org-delete-isolation-%s", uuid.NewString()))
	blockID := sha256hex(content)
	externalSHA1 := sha1hex(content)
	fileFSID := webFileFSID(t, []string{externalSHA1}, len(content))
	defaultRepo := createDisposableTestLibrary(t, adminClient, fmt.Sprintf("inttest-gc-isolation-default-%d", time.Now().UnixNano()))
	platformRepo := createDisposableTestLibrary(t, superadminClient, fmt.Sprintf("inttest-gc-isolation-platform-%d", time.Now().UnixNano()))
	defaultStore := newVerificationBlockStore(t, defaultOrgID)
	platformStore := newVerificationBlockStore(t, platformOrgID)

	t.Cleanup(func() {
		session := shareProjectionDBForTest(t).Session()
		removeTestLibraryFully(t, adminClient, session, defaultOrgID, defaultRepo)
		removeTestLibraryFully(t, superadminClient, session, platformOrgID, platformRepo)
	})
	cleanupUploadedBlockArtifactsForTest(t, defaultOrgID, defaultRepo, blockID, externalSHA1,
		db.BlockReferrerForFSObject(defaultRepo, fileFSID))
	cleanupUploadedBlockArtifactsForTest(t, platformOrgID, platformRepo, blockID, externalSHA1,
		db.BlockReferrerForFSObject(platformRepo, fileFSID))

	for _, upload := range []struct {
		client   *testClient
		repoID   string
		filename string
	}{
		{client: adminClient, repoID: defaultRepo, filename: "delete-me.bin"},
		{client: superadminClient, repoID: platformRepo, filename: "keep-me.bin"},
	} {
		resp := uploadFileViaBlocksFlow(t, upload.client, upload.repoID, "/", upload.filename, [][]byte{content}, false)
		expectStatus(t, resp, 200)
		resp.Body.Close()
		if got := downloadRepoFile(t, upload.client, upload.repoID, "/"+upload.filename); !bytes.Equal(got, content) {
			t.Fatalf("pre-delete download %s differs", upload.filename)
		}
	}

	session := shareProjectionDBForTest(t).Session()
	var defaultClass, platformClass string
	if err := session.Query(`SELECT storage_class FROM blocks WHERE org_id = ? AND block_id = ?`, defaultOrgID, blockID).Scan(&defaultClass); err != nil {
		t.Fatalf("read default block class: %v", err)
	}
	if err := session.Query(`SELECT storage_class FROM blocks WHERE org_id = ? AND block_id = ?`, platformOrgID, blockID).Scan(&platformClass); err != nil {
		t.Fatalf("read platform block class: %v", err)
	}
	if defaultClass == "" || defaultClass != platformClass {
		t.Fatalf("test requires one shared storage class, default=%q platform=%q", defaultClass, platformClass)
	}
	if defaultStore.StorageKeyForHash(blockID) == platformStore.StorageKeyForHash(blockID) {
		t.Fatal("precondition failed: org-scoped physical keys are equal")
	}
	for label, blockStore := range map[string]*storage.BlockStore{
		"default":  defaultStore,
		"platform": platformStore,
	} {
		if exists, err := blockStore.BlockExists(ctx, blockID); err != nil || !exists {
			t.Fatalf("%s physical object missing before GC: exists=%v err=%v", label, exists, err)
		}
	}

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	defaultUUID := uuid.MustParse(defaultOrgID)
	queuedItems, err := store.DequeueBatch(defaultUUID, 1, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("read canonical default-org GC queue: %v", err)
	}
	if len(queuedItems) != 0 {
		t.Fatalf("refusing private default-org drain with unrelated queued item: %+v", queuedItems[0])
	}

	trash := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", defaultRepo))
	expectStatus(t, trash, 200)
	trash.Body.Close()
	permanent := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/deleted/%s/", defaultRepo))
	expectStatus(t, permanent, 200)
	permanent.Body.Close()

	manager := storage.NewManager()
	manager.RegisterBackend(defaultClass, newVerificationS3Store(t), "")
	worker := gcpkg.NewWorker(store, gcpkg.NewStorageManagerAdapter(manager), gcpkg.NewQueue(store), 1, 0, false, &gcpkg.Stats{})
	defaultRepoUUID := uuid.MustParse(defaultRepo)

	deleted := pollUntil(t, 90*time.Second, 250*time.Millisecond, func() bool {
		items, err := store.DequeueBatch(defaultUUID, 1, time.Now())
		if err != nil {
			t.Fatalf("peek canonical default-org GC queue: %v", err)
		}
		if len(items) > 0 {
			item := items[0]
			owned := item.ItemID == defaultRepo || item.LibraryID == defaultRepoUUID || (item.ItemType == gcpkg.ItemBlock && item.ItemID == blockID)
			if !owned {
				t.Fatalf("refusing to process unrelated default-org GC item: %+v", item)
			}
		}
		if _, err := worker.ProcessOrgOnce(ctx, defaultUUID); err != nil {
			t.Fatalf("ProcessOrgOnce(default): %v", err)
		}
		canonicalExists, err := store.BlockExists(defaultUUID, blockID)
		if err != nil {
			t.Fatalf("default BlockExists: %v", err)
		}
		physicalExists, err := defaultStore.BlockExists(ctx, blockID)
		if err != nil {
			t.Fatalf("default S3 BlockExists: %v", err)
		}
		return !canonicalExists && !physicalExists
	})
	if !deleted {
		t.Fatal("default-org canonical row or physical object survived scoped GC drain")
	}

	platformUUID := uuid.MustParse(platformOrgID)
	if exists, err := store.BlockExists(platformUUID, blockID); err != nil || !exists {
		t.Fatalf("platform canonical block lost: exists=%v err=%v", exists, err)
	}
	if exists, err := platformStore.BlockExists(ctx, blockID); err != nil || !exists {
		t.Fatalf("platform physical block lost: exists=%v err=%v", exists, err)
	}
	platformRef := db.BlockReferrerForFSObject(platformRepo, fileFSID)
	var foundRef string
	if err := session.Query(`SELECT referrer FROM block_references WHERE org_id = ? AND block_id = ? AND referrer = ?`, platformOrgID, blockID, platformRef).Scan(&foundRef); err != nil {
		t.Fatalf("platform block reference lost: %v", err)
	}
	if got := downloadRepoFile(t, superadminClient, platformRepo, "/keep-me.bin"); !bytes.Equal(got, content) {
		t.Fatalf("platform download after default-org GC differs byte-for-byte")
	}
}

// discoverStorageClass uploads a real file and reads back the storage_class the
// server recorded for its block. Returns the class plus the real block id so the
// caller can confirm the verification BlockStore points at the same bucket the
// server writes to. Skips on bucket mismatch (e.g. non-docker env).
func discoverStorageClass(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-gc-s3disc-%d", time.Now().UnixNano()))
	orgID := resolveOrgID(t, repoID)
	bs := newVerificationBlockStore(t, orgID)
	_, realBlockID := uploadUniqueFile(t, adminClient, repoID, "discover.txt", "/")

	var storageClass string
	session := shareProjectionDBForTest(t).Session()
	if err := session.Query(
		`SELECT storage_class FROM blocks WHERE org_id = ? AND block_id = ?`,
		orgID, realBlockID,
	).Scan(&storageClass); err != nil {
		t.Fatalf("read storage_class for real block: %v", err)
	}
	if storageClass == "" {
		t.Skip("uploaded block has empty storage_class; cannot resolve a server-registered class; skipping")
	}

	// Self-check: the real block the server just wrote must be visible through our
	// verification BlockStore. If not, our env points at a different bucket than
	// the server and any S3 assertion below would be meaningless — skip instead.
	exists, err := bs.BlockExists(ctx, realBlockID)
	if err != nil {
		t.Fatalf("verification BlockStore lookup of real block: %v", err)
	}
	if !exists {
		t.Skipf("verification BlockStore cannot see server-written block %s; bucket mismatch, skipping", realBlockID[:16])
	}
	return storageClass
}

// seedSyntheticBlock uploads a unique object to MinIO and writes its canonical
// `blocks` row with NO references — a legitimate, ready-to-collect GC candidate
// under a synthetic org (so the live system never owned it).
func seedSyntheticBlock(t *testing.T, storageClass string) (uuid.UUID, string, *storage.BlockStore) {
	t.Helper()
	ctx := context.Background()

	content := []byte(fmt.Sprintf("gc-s3-deletion-%s\n", uuid.NewString()))
	sum := sha256.Sum256(content)
	blockID := hex.EncodeToString(sum[:])
	orgID := uuid.New()
	bs := newVerificationBlockStore(t, orgID.String())

	if _, err := bs.PutBlockData(ctx, &storage.BlockData{Hash: blockID, Data: content, Size: int64(len(content))}); err != nil {
		t.Fatalf("seed PutBlockData: %v", err)
	}
	if exists, err := bs.BlockExists(ctx, blockID); err != nil || !exists {
		t.Fatalf("seed object not present in S3 after upload (exists=%v err=%v)", exists, err)
	}

	session := shareProjectionDBForTest(t).Session()
	if err := session.Query(`
		INSERT INTO blocks (org_id, block_id, size_bytes, storage_class, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, orgID.String(), blockID, len(content), storageClass, time.Now().UTC()).Exec(); err != nil {
		t.Fatalf("seed canonical blocks row: %v", err)
	}
	return orgID, blockID, bs
}

// TestGC_BlockDeletion_RemovesObjectFromS3 verifies the happy path end-to-end:
// once GC processes an unreferenced block, the canonical Cassandra row is gone
// AND the physical object is gone from S3, with the recovery fence cleared.
func TestGC_BlockDeletion_RemovesObjectFromS3(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	ctx := context.Background()
	storageClass := discoverStorageClass(t)

	store := gcpkg.NewCassandraStore(shareProjectionDBForTest(t))
	orgID, blockID, bs := seedSyntheticBlock(t, storageClass)
	t.Cleanup(func() {
		_ = bs.DeleteBlock(ctx, blockID)
		_ = shareProjectionDBForTest(t).Session().Query(
			`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID.String(), blockID).Exec()
	})

	// Enqueue the block directly with a past timestamp so it clears the grace
	// period immediately; EnqueueItem also marks the org active so the real
	// server worker will dequeue it.
	if err := store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), gcpkg.ItemBlock, blockID, uuid.Nil, storageClass, 0); err != nil {
		t.Fatalf("EnqueueItem: %v", err)
	}

	// Drive the real server worker until the canonical row disappears.
	canonicalGone := pollUntil(t, 60*time.Second, time.Second, func() bool {
		triggerGCWorker(t)
		exists, err := store.BlockExists(orgID, blockID)
		if err != nil {
			t.Fatalf("BlockExists(canonical): %v", err)
		}
		return !exists
	})
	if !canonicalGone {
		t.Fatal("canonical blocks row still present after repeated GC worker runs")
	}

	// The physical object must actually be gone from S3 — the assertion the
	// rest of the suite never made. Allow a brief settle: the worker deletes
	// the row, then S3, then clears the orphan fence.
	objectGone := pollUntil(t, 30*time.Second, time.Second, func() bool {
		exists, err := bs.BlockExists(ctx, blockID)
		if err != nil {
			t.Fatalf("BlockExists(S3): %v", err)
		}
		return !exists
	})
	if !objectGone {
		t.Fatal("block object still exists in S3 after canonical row was deleted by GC")
	}

	// On a successful S3 delete the gc_s3_orphans recovery fence must be cleared.
	orphanCleared := pollUntil(t, 15*time.Second, time.Second, func() bool {
		_, found, err := shareProjectionDBForTest(t).GetBlockS3OrphanInfo(orgID.String(), blockID)
		if err != nil {
			t.Fatalf("GetBlockS3OrphanInfo: %v", err)
		}
		return !found
	})
	if !orphanCleared {
		t.Fatal("gc_s3_orphans fence left behind after successful S3 delete")
	}
}

// TestGC_S3OrphanRecovery_DeletesLingeringObject verifies the failure-recovery
// path: when a block's canonical row was already deleted but its S3 object
// lingered (DeleteBlock failed after the LWT step), the scanner's orphan
// recovery phase eventually removes the object and clears the fence.
func TestGC_S3OrphanRecovery_DeletesLingeringObject(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	ctx := context.Background()
	storageClass := discoverStorageClass(t)

	store := gcpkg.NewCassandraStore(shareProjectionDBForTest(t))

	// Build the exact post-failure state: object present in S3, gc_s3_orphans row
	// recorded, NO canonical blocks row (it was already finalized/deleted).
	content := []byte(fmt.Sprintf("gc-s3-orphan-%s\n", uuid.NewString()))
	sum := sha256.Sum256(content)
	blockID := hex.EncodeToString(sum[:])
	orgID := uuid.New()
	bs := newVerificationBlockStore(t, orgID.String())
	siblingOrgID := uuid.New()
	siblingStore := newVerificationBlockStore(t, siblingOrgID.String())

	if _, err := bs.PutBlockData(ctx, &storage.BlockData{Hash: blockID, Data: content, Size: int64(len(content))}); err != nil {
		t.Fatalf("seed PutBlockData: %v", err)
	}
	if _, err := siblingStore.PutBlockData(ctx, &storage.BlockData{Hash: blockID, Data: content, Size: int64(len(content))}); err != nil {
		t.Fatalf("seed sibling PutBlockData: %v", err)
	}
	if exists, err := bs.BlockExists(ctx, blockID); err != nil || !exists {
		t.Fatalf("seed orphan object not present in S3 (exists=%v err=%v)", exists, err)
	}
	t.Cleanup(func() {
		_ = bs.DeleteBlock(ctx, blockID)
		_ = siblingStore.DeleteBlock(ctx, blockID)
	})

	if _, err := store.RecordS3Orphan(orgID, blockID, storageClass, db.PlainBlockRepresentationID, "", "seed: simulated S3 delete failure", time.Now().UTC()); err != nil {
		t.Fatalf("RecordS3Orphan: %v", err)
	}
	if _, found, err := shareProjectionDBForTest(t).GetBlockS3OrphanInfo(orgID.String(), blockID); err != nil || !found {
		t.Fatalf("orphan fence not recorded (found=%v err=%v)", found, err)
	}

	// Drive the real scanner (phase 16 = S3 orphan recovery) until the object is
	// gone from S3.
	objectGone := pollUntil(t, 90*time.Second, 2*time.Second, func() bool {
		triggerGCScanner(t)
		exists, err := bs.BlockExists(ctx, blockID)
		if err != nil {
			t.Fatalf("BlockExists(S3): %v", err)
		}
		return !exists
	})
	if !objectGone {
		t.Fatal("orphaned object still exists in S3 after repeated scanner orphan-recovery runs")
	}
	if exists, err := siblingStore.BlockExists(ctx, blockID); err != nil || !exists {
		t.Fatalf("sibling org object was deleted by orphan recovery: exists=%v err=%v", exists, err)
	}
	if got, err := siblingStore.GetBlock(ctx, blockID); err != nil {
		t.Fatalf("read sibling org object: %v", err)
	} else if !bytes.Equal(got, content) {
		t.Fatalf("sibling org object changed: got %q want %q", got, content)
	}

	// Recovery must also clear the fence row once the object is gone.
	orphanCleared := pollUntil(t, 30*time.Second, 2*time.Second, func() bool {
		triggerGCScanner(t)
		_, found, err := shareProjectionDBForTest(t).GetBlockS3OrphanInfo(orgID.String(), blockID)
		if err != nil {
			t.Fatalf("GetBlockS3OrphanInfo: %v", err)
		}
		return !found
	})
	if !orphanCleared {
		t.Fatal("gc_s3_orphans fence not cleared after S3 orphan recovery")
	}
}
