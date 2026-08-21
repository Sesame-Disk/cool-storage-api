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

	"github.com/Sesame-Disk/sesamefs/internal/apikeys"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
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

// defaultClassS3Config resolves the namespace the local stack's default_class
// resolves to -- hot-minio-local in configs/config.docker.yaml, which Dockerfile
// bakes in as CONFIG_FILE -- following the same precedence the server itself uses:
// the per-class S3_CLASS_HOT_MINIO_LOCAL_* override when set, otherwise the value
// declared in that file, with credentials falling back to the generic keys exactly
// as applyStorageClassEnvOverrides does.
//
// Deliberately NOT S3_BUCKET/S3_ENDPOINT/S3_REGION: those configure the LEGACY
// "hot" backend, which the local stack keeps in its own separate bucket. The server
// under test writes through default_class, so a verification store that followed
// S3_BUCKET would be reading a bucket nothing ever wrote to.
func defaultClassS3Config() storage.S3Config {
	return storage.S3Config{
		Endpoint:        envOrDefault("S3_CLASS_HOT_MINIO_LOCAL_ENDPOINT", "http://minio:9000"),
		Bucket:          envOrDefault("S3_CLASS_HOT_MINIO_LOCAL_BUCKET", "sesamefs-blocks"),
		Region:          envOrDefault("S3_CLASS_HOT_MINIO_LOCAL_REGION", "us-east-1"),
		AccessKeyID:     envOrDefault("S3_CLASS_HOT_MINIO_LOCAL_ACCESS_KEY_ID", envOrDefault("S3_ACCESS_KEY_ID", "minioadmin")),
		SecretAccessKey: envOrDefault("S3_CLASS_HOT_MINIO_LOCAL_SECRET_ACCESS_KEY", envOrDefault("S3_SECRET_ACCESS_KEY", "minioadmin")),
		UsePathStyle:    true,
	}
}

func newVerificationS3Store(t *testing.T) *storage.S3Store {
	t.Helper()
	ctx := context.Background()
	s3cfg := defaultClassS3Config()
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

// isolatedTenant is a freshly created organization with an owner user and an
// API-key-backed client, exclusively owned by one test. Because the org is
// exclusive, its GC queue is private, so a scoped ProcessOrgOnce drains only this
// test's work. The shared default org cannot offer that: its gc_queue accumulates
// unrelated items across the full suite, so a private whole-org drain there trips
// the safety guard (the exact failure this test hit before — see
// ISSUE-GC-CROSS-ORG-BLOCK-DELETE-01 test notes).
type isolatedTenant struct {
	orgID  string
	userID string
	email  string
	client *testClient
}

// provisionIsolatedTenant creates a real tenant org + owner user via the superadmin
// admin API, then mints a real API key for that owner and returns a client bound to
// it. Admin key management is platform-org only, so the key is minted directly
// against the same Cassandra: the key hash is a plain SHA-256 with no server pepper
// (internal/apikeys), so a key created here validates in the running server exactly
// like one issued through the API, and the auth middleware accepts the raw key as a
// bearer token. Cleanup revokes the API key; the org+owner are removed by
// createAdminIdentityTestOrganization's soft-delete with eventual GC cascade
// (not an immediate hard-delete).
func provisionIsolatedTenant(t *testing.T, label string) *isolatedTenant {
	t.Helper()

	stamp := time.Now().UnixNano()
	name := fmt.Sprintf("inttest-gc-iso-%s-%d", label, stamp)
	email := fmt.Sprintf("inttest-gc-iso-%s-%d@sesamefs.local", label, stamp)

	// Creates the org + owner user and registers its own soft-delete cleanup.
	orgID := createAdminIdentityTestOrganization(t, name, email)

	userID, found := lookupUserIDByEmail(t, email)
	if !found {
		t.Fatalf("owner user_id not found for %s", email)
	}

	orgUUID, err := gocql.ParseUUID(orgID)
	if err != nil {
		t.Fatalf("parse org uuid %s: %v", orgID, err)
	}
	userUUID, err := gocql.ParseUUID(userID)
	if err != nil {
		t.Fatalf("parse user uuid %s: %v", userID, err)
	}

	mgr := apikeys.NewManager(shareProjectionDBForTest(t))
	// NewManager starts a background cleanupLoop goroutine; stop it when the test
	// ends (registered before CreateKey so it is cleaned up even if minting fails).
	t.Cleanup(mgr.Stop)
	rawToken, key, err := mgr.CreateKey(userUUID, orgUUID, "gc-iso-"+label, apikeys.ScopeReadWrite, nil)
	if err != nil {
		t.Fatalf("mint api key for %s: %v", email, err)
	}

	// The org+owner lifecycle is already cleaned by createAdminIdentityTestOrganization
	// (soft-delete via the admin API, which the docker GC then cascade-purges) — the
	// same pattern every admin-created test org uses. HardDeleteOrg is intentionally
	// NOT called here: it requires the org to already be in the deleted state with no
	// live children, which is not true at this point in the LIFO cleanup order. We only
	// revoke the API key, which the org soft-delete does not cover.
	t.Cleanup(func() {
		if err := mgr.RevokeKey(orgUUID, userUUID, key.KeyHash); err != nil {
			t.Logf("cleanup: revoke api key for %s: %v", email, err)
		}
	})

	return &isolatedTenant{orgID: orgID, userID: userID, email: email, client: newTestClient(baseURL, rawToken)}
}

func TestGC_CrossOrgIdenticalBlockDeleteIsolation(t *testing.T) {
	requireCassandra(t)

	ctx := context.Background()
	content := []byte(fmt.Sprintf("gc-cross-org-delete-isolation-%s", uuid.NewString()))
	blockID := sha256hex(content)
	externalSHA1 := sha1hex(content)
	fileFSID := webFileFSID(t, []string{externalSHA1}, len(content))

	// Two fresh, test-exclusive tenant orgs: one we delete + privately drain, one we
	// prove survives. Exclusive ownership is what makes the private drain of orgA's
	// GC queue sound (unlike the shared default org).
	orgA := provisionIsolatedTenant(t, "delete")
	orgB := provisionIsolatedTenant(t, "keep")

	repoA := createDisposableTestLibrary(t, orgA.client, fmt.Sprintf("inttest-gc-isolation-a-%d", time.Now().UnixNano()))
	repoB := createDisposableTestLibrary(t, orgB.client, fmt.Sprintf("inttest-gc-isolation-b-%d", time.Now().UnixNano()))
	storeA := newVerificationBlockStore(t, orgA.orgID)
	storeB := newVerificationBlockStore(t, orgB.orgID)

	t.Cleanup(func() {
		session := shareProjectionDBForTest(t).Session()
		removeTestLibraryFully(t, orgA.client, session, orgA.orgID, repoA)
		removeTestLibraryFully(t, orgB.client, session, orgB.orgID, repoB)
	})
	cleanupUploadedBlockArtifactsForTest(t, orgA.orgID, repoA, blockID, externalSHA1,
		db.BlockReferrerForFSObject(repoA, fileFSID))
	cleanupUploadedBlockArtifactsForTest(t, orgB.orgID, repoB, blockID, externalSHA1,
		db.BlockReferrerForFSObject(repoB, fileFSID))

	for _, upload := range []struct {
		tenant   *isolatedTenant
		repoID   string
		filename string
	}{
		{tenant: orgA, repoID: repoA, filename: "delete-me.bin"},
		{tenant: orgB, repoID: repoB, filename: "keep-me.bin"},
	} {
		resp := uploadFileViaBlocksFlow(t, upload.tenant.client, upload.repoID, "/", upload.filename, [][]byte{content}, false)
		expectStatus(t, resp, 200)
		resp.Body.Close()
		if got := downloadRepoFile(t, upload.tenant.client, upload.repoID, "/"+upload.filename); !bytes.Equal(got, content) {
			t.Fatalf("pre-delete download %s differs", upload.filename)
		}
	}

	session := shareProjectionDBForTest(t).Session()
	var classA, classB string
	if err := session.Query(`SELECT storage_class FROM blocks WHERE org_id = ? AND block_id = ?`, orgA.orgID, blockID).Scan(&classA); err != nil {
		t.Fatalf("read orgA block class: %v", err)
	}
	if err := session.Query(`SELECT storage_class FROM blocks WHERE org_id = ? AND block_id = ?`, orgB.orgID, blockID).Scan(&classB); err != nil {
		t.Fatalf("read orgB block class: %v", err)
	}
	if classA == "" || classA != classB {
		t.Fatalf("test requires one shared storage class, orgA=%q orgB=%q", classA, classB)
	}
	if storeA.StorageKeyForHash(blockID) == storeB.StorageKeyForHash(blockID) {
		t.Fatal("precondition failed: org-scoped physical keys are equal")
	}
	for label, blockStore := range map[string]*storage.BlockStore{
		"orgA": storeA,
		"orgB": storeB,
	} {
		if exists, err := blockStore.BlockExists(ctx, blockID); err != nil || !exists {
			t.Fatalf("%s physical object missing before GC: exists=%v err=%v", label, exists, err)
		}
	}

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	// Successful publication deliberately leaves the session's up: reference to
	// Cassandra TTL. Prove that new contract, then stage the state Cassandra will
	// produce after that TTL retires the row before testing permanent-delete GC.
	// The raw delete below is test-only clock advancement after the TTL assertion;
	// no production success or rollback path releases the provisional row.
	var provisionalReferrer string
	iter := session.Query(`
		SELECT referrer FROM block_references WHERE org_id = ? AND block_id = ?
	`, orgA.orgID, blockID).Iter()
	var referrer string
	for iter.Scan(&referrer) {
		if len(referrer) >= len("up:") && referrer[:len("up:")] == "up:" {
			if provisionalReferrer != "" {
				t.Fatalf("orgA upload created multiple provisional refs: %q and %q", provisionalReferrer, referrer)
			}
			provisionalReferrer = referrer
		}
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("list orgA block references: %v", err)
	}
	if provisionalReferrer == "" {
		t.Fatal("successful orgA upload did not retain its TTL-bound provisional reference")
	}
	var provisionalTTL int
	if err := session.Query(`
		SELECT TTL(library_id) FROM block_references
		WHERE org_id = ? AND block_id = ? AND referrer = ?
	`, orgA.orgID, blockID, provisionalReferrer).Scan(&provisionalTTL); err != nil || provisionalTTL <= 0 {
		t.Fatalf("orgA provisional reference TTL = %d, err=%v; want a retained TTL-bound row", provisionalTTL, err)
	}
	if err := session.Query(`
		DELETE FROM block_references
		WHERE org_id = ? AND block_id = ? AND referrer = ?
	`, orgA.orgID, blockID, provisionalReferrer).Exec(); err != nil {
		t.Fatalf("stage orgA provisional reference after TTL retirement: %v", err)
	}
	var retiredLibraryID string
	if err := session.Query(`
		SELECT library_id FROM block_references
		WHERE org_id = ? AND block_id = ? AND referrer = ?
	`, orgA.orgID, blockID, provisionalReferrer).Scan(&retiredLibraryID); err != gocql.ErrNotFound {
		t.Fatalf("provisional reference after staged TTL retirement: library=%q err=%v", retiredLibraryID, err)
	}

	orgAUUID := mustParseUUID(t, orgA.orgID)
	queuedItems, err := store.DequeueBatch(orgAUUID, 1, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("read canonical orgA GC queue: %v", err)
	}
	if len(queuedItems) != 0 {
		t.Fatalf("exclusive orgA queue unexpectedly non-empty before delete: %+v", queuedItems[0])
	}

	trash := orgA.client.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoA))
	expectStatus(t, trash, 200)
	trash.Body.Close()
	permanent := orgA.client.Delete(t, fmt.Sprintf("/api/v2.1/repos/deleted/%s/", repoA))
	expectStatus(t, permanent, 200)
	permanent.Body.Close()

	manager := storage.NewManager()
	manager.RegisterBackend(classA, newVerificationS3Store(t), "")
	worker := gcpkg.NewWorker(store, gcpkg.NewStorageManagerAdapter(manager), gcpkg.NewQueue(store), 1, 0, false, &gcpkg.Stats{})
	repoAUUID := mustParseUUID(t, repoA)

	deleted := pollUntil(t, 90*time.Second, 250*time.Millisecond, func() bool {
		// orgA is exclusive to this test, so every item in its queue is ours. The
		// ownership check is defence-in-depth against a stray enqueue.
		items, err := store.DequeueBatch(orgAUUID, 1, time.Now())
		if err != nil {
			t.Fatalf("peek canonical orgA GC queue: %v", err)
		}
		if len(items) > 0 {
			item := items[0]
			owned := item.ItemID == repoA || item.LibraryID == repoAUUID || (item.ItemType == gcpkg.ItemBlock && item.ItemID == blockID)
			if !owned {
				t.Fatalf("unexpected foreign item in exclusive orgA queue: %+v", item)
			}
		}
		if _, err := worker.ProcessOrgOnce(ctx, orgAUUID); err != nil {
			t.Fatalf("ProcessOrgOnce(orgA): %v", err)
		}
		canonicalExists, err := store.BlockExists(orgAUUID, blockID)
		if err != nil {
			t.Fatalf("orgA BlockExists: %v", err)
		}
		physicalExists, err := storeA.BlockExists(ctx, blockID)
		if err != nil {
			t.Fatalf("orgA S3 BlockExists: %v", err)
		}
		return !canonicalExists && !physicalExists
	})
	if !deleted {
		t.Fatal("orgA canonical row or physical object survived scoped GC drain")
	}

	orgBUUID := mustParseUUID(t, orgB.orgID)
	if exists, err := store.BlockExists(orgBUUID, blockID); err != nil || !exists {
		t.Fatalf("orgB canonical block lost: exists=%v err=%v", exists, err)
	}
	if exists, err := storeB.BlockExists(ctx, blockID); err != nil || !exists {
		t.Fatalf("orgB physical block lost: exists=%v err=%v", exists, err)
	}
	orgBRef := db.BlockReferrerForFSObject(repoB, fileFSID)
	var foundRef string
	if err := session.Query(`SELECT referrer FROM block_references WHERE org_id = ? AND block_id = ? AND referrer = ?`, orgB.orgID, blockID, orgBRef).Scan(&foundRef); err != nil {
		t.Fatalf("orgB block reference lost: %v", err)
	}
	if got := downloadRepoFile(t, orgB.client, repoB, "/keep-me.bin"); !bytes.Equal(got, content) {
		t.Fatalf("orgB download after orgA GC differs byte-for-byte")
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
		INSERT INTO blocks (org_id, block_id, size_bytes, storage_class, storage_key, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, orgID.String(), blockID, len(content), storageClass, bs.StorageKeyForHash(blockID), time.Now().UTC()).Exec(); err != nil {
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

	seedS3OrphanWithStorageKey(t, store, orgID, blockID, bs.StorageKeyForHash(blockID), storageClass, "", "seed: simulated S3 delete failure", time.Now().UTC())
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
