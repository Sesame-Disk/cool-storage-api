//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	v2 "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/google/uuid"
)

func TestNeedsPutUsesCanonicalMinIOBucket(t *testing.T) {
	ctx := context.Background()
	newStore := func(bucket string) *storage.S3Store {
		t.Helper()
		store, err := storage.NewS3Store(ctx, storage.S3Config{
			Endpoint:        envOrDefault("S3_ENDPOINT", "http://minio:9000"),
			Bucket:          bucket,
			Region:          envOrDefault("S3_REGION", "us-east-1"),
			AccessKeyID:     envOrDefault("S3_ACCESS_KEY_ID", "minioadmin"),
			SecretAccessKey: envOrDefault("S3_SECRET_ACCESS_KEY", "minioadmin"),
			UsePathStyle:    true,
		})
		if err != nil {
			t.Fatalf("NewS3Store(%s): %v", bucket, err)
		}
		if err := store.HeadBucket(ctx); err != nil {
			t.Skipf("MinIO bucket %s unavailable: %v", bucket, err)
		}
		return store
	}

	preferredBucket := envOrDefault("S3_TEST_PREFERRED_BUCKET", "sesamefs-eu")
	canonicalBucket := envOrDefault("S3_TEST_CANONICAL_BUCKET", "sesamefs-usa")
	preferredS3 := newStore(preferredBucket)
	canonicalS3 := newStore(canonicalBucket)
	manager := storage.NewManager()
	manager.RegisterBackend("preferred", preferredS3, "")
	manager.RegisterBackend("canonical", canonicalS3, "")

	orgID := uuid.NewString()
	preferredStore, err := manager.GetBlockStoreForOrg(orgID, "preferred")
	if err != nil {
		t.Fatalf("preferred block store: %v", err)
	}
	canonicalStore, err := manager.GetBlockStoreForOrg(orgID, "canonical")
	if err != nil {
		t.Fatalf("canonical block store: %v", err)
	}
	data := []byte("canonical NeedsPut integration payload")
	hashBytes := sha256.Sum256(data)
	blockID := hex.EncodeToString(hashBytes[:])
	t.Cleanup(func() {
		if err := preferredStore.DeleteBlock(ctx, blockID); err != nil {
			t.Errorf("cleanup preferred object: %v", err)
		}
		if err := canonicalStore.DeleteBlock(ctx, blockID); err != nil {
			t.Errorf("cleanup canonical object: %v", err)
		}
	})

	canonicalKey := canonicalStore.StorageKeyForHash(blockID)
	database := shareProjectionDBForTest(t)
	referrer := db.BlockReferrerForUpload(uuid.NewString())
	if err := database.UpsertBlockMetadata(orgID, blockID, len(data), "canonical", canonicalKey); err != nil {
		t.Fatalf("seed canonical block metadata: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Session().Query(`DELETE FROM block_references WHERE org_id = ? AND block_id = ?`, orgID, blockID).Exec(); err != nil {
			t.Errorf("cleanup block references: %v", err)
		}
		if err := database.Session().Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Exec(); err != nil {
			t.Errorf("cleanup block metadata: %v", err)
		}
	})

	probe, err := database.ProbeBlockReuse(orgID, blockID)
	if err != nil {
		t.Fatalf("ProbeBlockReuse(NeedsPut): %v", err)
	}
	if probe.Decision != db.BlockReuseNeedsPut || probe.StorageClass != "canonical" || probe.StorageKey != canonicalKey {
		t.Fatalf("probe = %+v, want existing canonical NeedsPut metadata", probe)
	}
	storedKey, storedClass, didPut, err := v2.StoreUploadedBlockForProbe(ctx, blockID, probe, data, manager, preferredStore, "preferred", orgID, nil)
	if err != nil {
		t.Fatalf("StoreUploadedBlockForProbe(): %v", err)
	}
	if !didPut || storedClass != "canonical" || storedKey != canonicalKey {
		t.Fatalf("didPut/class/key = %v/%q/%q, want true/canonical/%q", didPut, storedClass, storedKey, canonicalKey)
	}
	canonicalExists, err := canonicalStore.BlockExists(ctx, blockID)
	if err != nil {
		t.Fatalf("canonical BlockExists: %v", err)
	}
	preferredExists, err := preferredStore.BlockExists(ctx, blockID)
	if err != nil {
		t.Fatalf("preferred BlockExists: %v", err)
	}
	if !canonicalExists || preferredExists {
		t.Fatalf("canonical/preferred existence = %v/%v, want true/false", canonicalExists, preferredExists)
	}

	if err := database.AddBlockReference(orgID, blockID, referrer, uuid.NewString(), db.ProvisionalBlockReferenceTTLSeconds); err != nil {
		t.Fatalf("add reusable reference: %v", err)
	}
	reusableProbe, err := database.ProbeBlockReuse(orgID, blockID)
	if err != nil {
		t.Fatalf("ProbeBlockReuse(Reusable): %v", err)
	}
	if reusableProbe.Decision != db.BlockReuseReusable {
		t.Fatalf("reusable probe = %+v, want Reusable", reusableProbe)
	}
	if exists, err := v2.CanonicalBlockExistsForProbe(ctx, blockID, reusableProbe, manager, preferredStore, "preferred", orgID); err != nil || !exists {
		t.Fatalf("CanonicalBlockExistsForProbe() = %v, %v; want true, nil", exists, err)
	}
}

func TestLegacyBlockUploadRepairsCanonicalMinIOBucket(t *testing.T) {
	ctx := context.Background()
	newStore := func(bucket string) *storage.BlockStore {
		t.Helper()
		s3Store, err := storage.NewS3Store(ctx, storage.S3Config{
			Endpoint:        envOrDefault("S3_ENDPOINT", "http://minio:9000"),
			Bucket:          bucket,
			Region:          envOrDefault("S3_REGION", "us-east-1"),
			AccessKeyID:     envOrDefault("S3_ACCESS_KEY_ID", "minioadmin"),
			SecretAccessKey: envOrDefault("S3_SECRET_ACCESS_KEY", "minioadmin"),
			UsePathStyle:    true,
		})
		if err != nil {
			t.Fatalf("NewS3Store(%s): %v", bucket, err)
		}
		blockStore, err := storage.NewOrgBlockStore(s3Store, "blocks/", defaultOrgID)
		if err != nil {
			t.Fatalf("NewOrgBlockStore(%s): %v", bucket, err)
		}
		return blockStore
	}

	preferredStore := newStore(envOrDefault("S3_BUCKET", "sesamefs-blocks"))
	canonicalStore := newStore(envOrDefault("S3_TEST_CANONICAL_BUCKET", "sesamefs-usa"))
	data := []byte("legacy endpoint canonical repair")
	hashBytes := sha256.Sum256(data)
	blockID := hex.EncodeToString(hashBytes[:])
	database := shareProjectionDBForTest(t)
	canonicalKey := canonicalStore.StorageKeyForHash(blockID)
	if err := database.UpsertBlockMetadata(defaultOrgID, blockID, len(data), "hot-s3-usa", canonicalKey); err != nil {
		t.Fatalf("seed canonical metadata: %v", err)
	}
	t.Cleanup(func() {
		_ = preferredStore.DeleteBlock(ctx, blockID)
		_ = canonicalStore.DeleteBlock(ctx, blockID)
		_ = database.Session().Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, defaultOrgID, blockID).Exec()
	})

	status, body := uploadRawBlockStatus(t, adminClient, data)
	if status != 201 {
		t.Fatalf("legacy canonical repair status = %d, want 201; body=%v", status, body)
	}
	canonicalExists, err := canonicalStore.BlockExists(ctx, blockID)
	if err != nil {
		t.Fatalf("canonical BlockExists: %v", err)
	}
	preferredExists, err := preferredStore.BlockExists(ctx, blockID)
	if err != nil {
		t.Fatalf("preferred BlockExists: %v", err)
	}
	if !canonicalExists || preferredExists {
		t.Fatalf("canonical/preferred existence = %v/%v, want true/false", canonicalExists, preferredExists)
	}
	var provisionalReferrer string
	if err := database.Session().Query(`
		SELECT referrer FROM block_references
		WHERE org_id = ? AND block_id = ?
		LIMIT 1
	`, defaultOrgID, blockID).Scan(&provisionalReferrer); err != nil {
		t.Fatalf("legacy provisional reference: %v", err)
	}
	if !strings.HasPrefix(provisionalReferrer, "up:legacy-block:") {
		t.Fatalf("legacy referrer = %q, want up:legacy-block prefix", provisionalReferrer)
	}
	t.Cleanup(func() {
		var expiresAt time.Time
		if err := database.Session().Query(`
			SELECT expires_at FROM gc_provisional_block_refs
			WHERE org_id = ? AND block_id = ? AND referrer = ?
		`, defaultOrgID, blockID, provisionalReferrer).Scan(&expiresAt); err == nil {
			_ = database.DeleteProvisionalBlockReferenceExpiry(defaultOrgID, blockID, provisionalReferrer, expiresAt)
		}
		_ = database.RemoveBlockReference(defaultOrgID, blockID, provisionalReferrer)
	})

	resp := adminClient.PostJSON(t, "/api/v2/blocks/check", map[string]interface{}{"hashes": []string{blockID}})
	expectStatus(t, resp, 200)
	var checked struct {
		Existing []string `json:"existing"`
		Missing  []string `json:"missing"`
	}
	decodeJSON(t, resp, &checked)
	if len(checked.Existing) != 1 || checked.Existing[0] != blockID || len(checked.Missing) != 0 {
		t.Fatalf("check existing/missing = %v/%v, want [%s]/[]", checked.Existing, checked.Missing, blockID)
	}
}

func TestProvisionalBlockReferenceTTLIsCleanupAuthority(t *testing.T) {
	database := shareProjectionDBForTest(t)
	orgID := uuid.New()
	blockID := uuid.NewString()
	referrer := db.BlockReferrerForUpload(uuid.NewString())
	expiresAt := time.Now().Add(2 * time.Second).UTC().Truncate(time.Millisecond)
	store := gcpkg.NewCassandraStore(database)

	t.Cleanup(func() {
		_ = database.Session().Query(`DELETE FROM block_references WHERE org_id = ? AND block_id = ?`, orgID, blockID).Exec()
		_ = database.Session().Query(`DELETE FROM gc_provisional_block_refs WHERE org_id = ? AND block_id = ? AND referrer = ?`, orgID, blockID, referrer).Exec()
		_ = database.Session().Query(`
			DELETE FROM gc_provisional_block_refs_by_day
			WHERE expiry_day = ? AND bucket = ? AND expires_at = ? AND org_id = ? AND block_id = ? AND referrer = ?
		`, db.GCProjectionUTCDate(expiresAt), db.GCDiscoveryBucket(orgID.String(), blockID, referrer), expiresAt, orgID, blockID, referrer).Exec()
	})

	if err := database.AddProvisionalBlockReferenceWithExpiry(orgID.String(), blockID, referrer, uuid.NewString(), "hot", expiresAt, 2); err != nil {
		t.Fatalf("AddProvisionalBlockReferenceWithExpiry(): %v", err)
	}
	if exists, err := store.BlockReferenceExists(orgID, blockID, referrer); err != nil || !exists {
		t.Fatalf("BlockReferenceExists() before TTL = %v, %v; want true, nil", exists, err)
	}
	time.Sleep(3 * time.Second)
	if exists, err := store.BlockReferenceExists(orgID, blockID, referrer); err != nil || exists {
		t.Fatalf("BlockReferenceExists() after TTL = %v, %v; want false, nil", exists, err)
	}
}
