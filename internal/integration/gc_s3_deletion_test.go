//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/google/uuid"
)

// These tests close the one gap the rest of the GC suite never covered: that a
// block deleted through the GC paths is *actually* gone from the object store
// (MinIO), not merely removed from Cassandra. Every other GC test asserts on
// Cassandra rows only; nothing ever queried the bucket.
//
// They drive the real server worker/scanner through the admin API (the same
// code path production runs) and verify the physical object via an independent
// BlockStore pointed at the same dev MinIO bucket. We never run a private worker
// here, so we cannot disturb the live queue's global state.

// newVerificationBlockStore builds a BlockStore pointed at the dev MinIO bucket
// for direct object existence checks. It mirrors the server's block key layout
// (NewBlockStore(s3, "blocks/")). Skips the test when MinIO is unreachable.
func newVerificationBlockStore(t *testing.T) *storage.BlockStore {
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
	return storage.NewBlockStore(s3Store, "blocks/")
}

// discoverStorageClass uploads a real file and reads back the storage_class the
// server recorded for its block. Returns the class plus the real block id so the
// caller can confirm the verification BlockStore points at the same bucket the
// server writes to. Skips on bucket mismatch (e.g. non-docker env).
func discoverStorageClass(t *testing.T, bs *storage.BlockStore) string {
	t.Helper()
	ctx := context.Background()

	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-gc-s3disc-%d", time.Now().UnixNano()))
	orgID := resolveOrgID(t, repoID)
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
func seedSyntheticBlock(t *testing.T, bs *storage.BlockStore, storageClass string) (uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()

	content := []byte(fmt.Sprintf("gc-s3-deletion-%s\n", uuid.NewString()))
	sum := sha256.Sum256(content)
	blockID := hex.EncodeToString(sum[:])
	orgID := uuid.New()

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
	return orgID, blockID
}

// TestGC_BlockDeletion_RemovesObjectFromS3 verifies the happy path end-to-end:
// once GC processes an unreferenced block, the canonical Cassandra row is gone
// AND the physical object is gone from S3, with the recovery fence cleared.
func TestGC_BlockDeletion_RemovesObjectFromS3(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	ctx := context.Background()
	bs := newVerificationBlockStore(t)
	storageClass := discoverStorageClass(t, bs)

	store := gcpkg.NewCassandraStore(shareProjectionDBForTest(t))
	orgID, blockID := seedSyntheticBlock(t, bs, storageClass)
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
	bs := newVerificationBlockStore(t)
	storageClass := discoverStorageClass(t, bs)

	store := gcpkg.NewCassandraStore(shareProjectionDBForTest(t))

	// Build the exact post-failure state: object present in S3, gc_s3_orphans row
	// recorded, NO canonical blocks row (it was already finalized/deleted).
	content := []byte(fmt.Sprintf("gc-s3-orphan-%s\n", uuid.NewString()))
	sum := sha256.Sum256(content)
	blockID := hex.EncodeToString(sum[:])
	orgID := uuid.New()

	if _, err := bs.PutBlockData(ctx, &storage.BlockData{Hash: blockID, Data: content, Size: int64(len(content))}); err != nil {
		t.Fatalf("seed PutBlockData: %v", err)
	}
	if exists, err := bs.BlockExists(ctx, blockID); err != nil || !exists {
		t.Fatalf("seed orphan object not present in S3 (exists=%v err=%v)", exists, err)
	}
	t.Cleanup(func() { _ = bs.DeleteBlock(ctx, blockID) })

	if _, err := store.RecordS3Orphan(orgID, blockID, storageClass, "", "seed: simulated S3 delete failure", time.Now().UTC()); err != nil {
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
