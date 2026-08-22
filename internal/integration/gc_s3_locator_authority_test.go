//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/google/uuid"
)

// gcQueueRetryCountForTest scans every queue bucket for one item and returns its
// retry count. It is the evidence that the worker actually reached the item: a
// refusal that is indistinguishable from "never processed" would make the
// no-deletion assertions below vacuous.
func gcQueueRetryCountForTest(t *testing.T, orgID uuid.UUID, itemType gcpkg.ItemType, itemID string) (int, bool) {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	for bucket := 0; bucket < 32; bucket++ {
		iter := session.Query(`
			SELECT item_type, item_id, retry_count FROM gc_queue WHERE org_id = ? AND bucket = ?
		`, orgID.String(), bucket).Iter()
		var gotType, gotID string
		var retryCount int
		for iter.Scan(&gotType, &gotID, &retryCount) {
			if gotType == string(itemType) && gotID == itemID {
				_ = iter.Close()
				return retryCount, true
			}
		}
		if err := iter.Close(); err != nil {
			t.Fatalf("scan gc_queue bucket %d: %v", bucket, err)
		}
	}
	return 0, false
}

func gcFailedItemExistsForTest(t *testing.T, orgID uuid.UUID, itemType gcpkg.ItemType, itemID string) bool {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	iter := session.Query(`SELECT item_type, item_id FROM gc_failed_items WHERE org_id = ?`, orgID.String()).Iter()
	var gotType, gotID string
	for iter.Scan(&gotType, &gotID) {
		if gotType == string(itemType) && gotID == itemID {
			_ = iter.Close()
			return true
		}
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("scan gc_failed_items: %v", err)
	}
	return false
}

// TestGC_BlockDeletion_RefusesForeignStorageKey is the end-to-end half of the
// locator-authority guard: real Cassandra, real MinIO, real BlockStores.
//
// A canonical row whose storage_key names ANOTHER organization object is what a
// corrupt write, a bad backfill, or a future key-minting writer would leave
// behind. The store applies whatever key it is handed, so without the guard this
// row destroys the other tenant bytes for real. The unit test proves the
// refusal; this one proves the object is still in the bucket afterwards.
func TestGC_BlockDeletion_RefusesForeignStorageKey(t *testing.T) {
	requireCassandra(t)

	ctx := context.Background()
	storageClass := discoverStorageClass(t)
	database := shareProjectionDBForTest(t)
	session := database.Session()
	store := gcpkg.NewCassandraStore(database)

	content := fmt.Appendf(nil, "gc-foreign-locator-%s\n", uuid.NewString())
	blockID := sha256hex(content)
	orgID := uuid.New()
	victimOrgID := uuid.New()
	collected := newVerificationBlockStore(t, orgID.String())
	victim := newVerificationBlockStore(t, victimOrgID.String())

	foreignKey := victim.StorageKeyForHash(blockID)
	ownKey := collected.StorageKeyForHash(blockID)
	if foreignKey == ownKey {
		t.Fatal("precondition failed: org-scoped keys for the two orgs are equal")
	}

	// Registered BEFORE anything is created: a failure between the two PUTs or at
	// the INSERT would otherwise strand objects in the bucket.
	//
	// This test leaves more behind than a passing one would. Its queue item never
	// completes by design, so nothing removes the gc_pending_items row (no TTL on
	// that table), and the org keeps its gc_active_orgs/gc_dirty_orgs markers: the
	// worker only drops an org from the active set when a batch comes back short
	// (`len(items) < batchSize`), and with batchSize=1 the retried item keeps every
	// batch full. cleanupGCBlockFixturesForTest covers all of it.
	t.Cleanup(func() {
		cleanupGCBlockFixturesForTest(t, orgID, blockID)
		if err := session.Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID.String(), blockID).Exec(); err != nil {
			t.Fatalf("cleanup blocks row: %v", err)
		}
		_ = collected.DeleteBlockByStorageKey(ctx, ownKey)
		_ = victim.DeleteBlockByStorageKey(ctx, foreignKey)
	})

	for label, blockStore := range map[string]*storage.BlockStore{"victim": victim, "collected": collected} {
		if _, err := blockStore.PutBlockData(ctx, &storage.BlockData{Hash: blockID, Data: content, Size: int64(len(content))}); err != nil {
			t.Fatalf("seed %s object: %v", label, err)
		}
	}

	// The corrupt row: this block, in this org, pointing at the victim object.
	if err := session.Query(`
		INSERT INTO blocks (org_id, block_id, size_bytes, storage_class, storage_key, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, orgID.String(), blockID, len(content), storageClass, foreignKey, time.Now().UTC()).Exec(); err != nil {
		t.Fatalf("seed corrupt canonical blocks row: %v", err)
	}

	queuedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	if err := store.EnqueueItem(orgID, queuedAt, gcpkg.ItemBlock, blockID, uuid.Nil, storageClass, 0); err != nil {
		t.Fatalf("EnqueueItem: %v", err)
	}

	// Drive an in-process worker wired to the real storage manager, so the run is
	// deterministic rather than dependent on which node holds the GC lease.
	manager := storage.NewManager()
	manager.RegisterBackend(storageClass, newVerificationS3Store(t), "")
	worker := gcpkg.NewWorker(store, gcpkg.NewStorageManagerAdapter(manager), gcpkg.NewQueue(store), 1, 0, false, &gcpkg.Stats{})

	attempted := pollUntil(t, 30*time.Second, 500*time.Millisecond, func() bool {
		if _, err := worker.ProcessOrgOnce(ctx, orgID); err != nil {
			t.Fatalf("ProcessOrgOnce: %v", err)
		}
		if retryCount, found := gcQueueRetryCountForTest(t, orgID, gcpkg.ItemBlock, blockID); found && retryCount > 0 {
			return true
		}
		// A retry-capped item leaves the queue for the DLQ. Checking that explicitly
		// rather than treating "absent from the queue" as success keeps a vanished
		// item — never enqueued, cleaned up by something else — from counting as a
		// refusal the worker actually performed.
		return gcFailedItemExistsForTest(t, orgID, gcpkg.ItemBlock, blockID)
	})
	if !attempted {
		t.Fatal("GC worker never processed the queued block; the assertions below would prove nothing")
	}

	// Nothing may have been destroyed, least of all the other tenant object.
	if exists, err := victim.BlockExists(ctx, blockID); err != nil || !exists {
		t.Fatalf("victim object was deleted through a foreign locator (exists=%v err=%v)", exists, err)
	}
	if exists, err := collected.BlockExists(ctx, blockID); err != nil || !exists {
		t.Fatalf("own object was deleted despite the refusal (exists=%v err=%v)", exists, err)
	}

	rowExists, err := store.BlockExists(orgID, blockID)
	if err != nil {
		t.Fatalf("BlockExists(canonical): %v", err)
	}
	if !rowExists {
		t.Fatal("canonical row was deleted for a block whose object GC refused to touch")
	}

	var gcState, gcClaimID string
	if err := session.Query(`
		SELECT gc_state, gc_claim_id FROM blocks WHERE org_id = ? AND block_id = ?
	`, orgID.String(), blockID).Scan(&gcState, &gcClaimID); err != nil {
		t.Fatalf("read claim state: %v", err)
	}
	if gcState != "" || gcClaimID != "" {
		t.Fatalf("claim must be released so the block is not fenced forever, got state=%q claim=%q", gcState, gcClaimID)
	}

	if _, found, err := database.GetBlockS3OrphanInfo(orgID.String(), blockID); err != nil {
		t.Fatalf("GetBlockS3OrphanInfo: %v", err)
	} else if found {
		t.Fatal("a recovery row was recorded for a refused delete; a later sweep would inherit the same foreign locator")
	}
}
