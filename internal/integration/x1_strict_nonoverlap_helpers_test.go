//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	v2pkg "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

func x1BlockID(seed string) string {
	digest := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(digest[:])
}

func x1Attempt(target gcpkg.BlockDeleteTarget, label string) gcpkg.BlockDeleteAuthority {
	return gcpkg.BlockDeleteAuthority{
		Target:    target,
		ClaimID:   "x1-" + label + "-" + uuid.NewString(),
		ClaimedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
}

func x1Cleanup(t *testing.T, database *dbpkg.DB, orgID uuid.UUID, blockID string, referrers ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, referrer := range referrers {
			_ = database.DeleteProvisionalBlockReferenceExpiry(orgID.String(), blockID, referrer, time.Time{})
			_ = database.RemoveBlockReference(orgID.String(), blockID, referrer)
		}
		_ = database.Session().Query(`DELETE FROM block_references WHERE org_id = ? AND block_id = ?`, orgID.String(), blockID).Exec()
		cleanupGCBlockFixturesForTest(t, orgID, blockID)
		_ = database.Session().Query(`DELETE FROM gc_block_delete_lifecycles WHERE org_id = ? AND block_id = ?`, orgID.String(), blockID).Exec()
		_ = database.Session().Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID.String(), blockID).Exec()
	})
}

func x1ClaimAcquired(t *testing.T, store *gcpkg.CassandraStore, orgID uuid.UUID, blockID string, attempt gcpkg.BlockDeleteAuthority) {
	t.Helper()
	claim, err := store.ClaimBlockDelete(orgID, blockID, attempt)
	if err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("ClaimBlockDelete = %s, %v; want acquired", claim.Outcome, err)
	}
}

func x1CommitHandoffAfterZeroRefs(t *testing.T, store *gcpkg.CassandraStore, orgID uuid.UUID, blockID string, attempt gcpkg.BlockDeleteAuthority) gcpkg.BlockDeleteAuthority {
	t.Helper()
	x1ClaimAcquired(t, store, orgID, blockID, attempt)
	hasRefs, err := store.BlockHasReferencesGlobal(orgID, blockID)
	if err != nil || hasRefs {
		t.Fatalf("authorizing EACH_QUORUM refs = %v, %v; want false, nil", hasRefs, err)
	}
	handoff, err := store.CommitBlockDeleteOrphanHandoff(orgID, blockID, attempt)
	if err != nil || (handoff.Outcome != gcpkg.BlockDeleteHandoffCommitted && handoff.Outcome != gcpkg.BlockDeleteHandoffAlreadyCommitted) {
		t.Fatalf("CommitBlockDeleteOrphanHandoff = %s, %v; want committed", handoff.Outcome, err)
	}
	if !handoff.Authority.IsZero() {
		return handoff.Authority.Authority()
	}
	return attempt
}

func x1ReadCommittedRow(t *testing.T, database *dbpkg.DB, orgID uuid.UUID, blockID string) (gcState, claimID string, handoff bool, storageClass, storageKey string) {
	t.Helper()
	var handoffFlag *bool
	err := database.Session().Query(
		`SELECT gc_state, gc_claim_id, gc_orphan_handoff, storage_class, storage_key FROM blocks WHERE org_id = ? AND block_id = ?`,
		orgID.String(), blockID,
	).Scan(&gcState, &claimID, &handoffFlag, &storageClass, &storageKey)
	if err != nil {
		t.Fatalf("read committed blocks row %s/%s: %v", orgID, blockID, err)
	}
	return gcState, claimID, handoffFlag != nil && *handoffFlag, storageClass, storageKey
}

func x1AssertCanonicalPresent(t *testing.T, store *gcpkg.CassandraStore, orgID uuid.UUID, blockID, wantKey string) {
	t.Helper()
	info, err := store.GetBlockInfo(orgID, blockID)
	if err != nil {
		t.Fatalf("GetBlockInfo(%s): %v", blockID, err)
	}
	if info.StorageKey != wantKey {
		t.Fatalf("canonical storage_key = %q, want %q", info.StorageKey, wantKey)
	}
}

func x1AssertCanonicalAbsent(t *testing.T, store *gcpkg.CassandraStore, orgID uuid.UUID, blockID string) {
	t.Helper()
	_, err := store.GetBlockInfo(orgID, blockID)
	if !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("GetBlockInfo after finalize = %v, want ErrNotFound", err)
	}
}

func x1SeedPhysical(t *testing.T, database *dbpkg.DB, blockStore *storage.BlockStore, orgID uuid.UUID, content []byte, storageClass string) (blockID, storageKey string) {
	t.Helper()
	digest := sha256.Sum256(content)
	blockID = hex.EncodeToString(digest[:])
	key, err := blockStore.MintStorageKey(blockID)
	if err != nil {
		t.Fatalf("MintStorageKey: %v", err)
	}
	if _, err := blockStore.PutObjectAutoDirect(t.Context(), key, content); err != nil {
		t.Fatalf("seed PUT %s: %v", key, err)
	}
	installed := database.InstallBlockMetadata(t.Context(), orgID.String(), dbpkg.PlainBlockRepresentationID, blockID, "", len(content), dbpkg.BlockPhysicalLocation{
		StorageClass: storageClass,
		StorageKey:   key,
	})
	if installed.Outcome != dbpkg.InstallBlockMetadataApplied {
		t.Fatalf("InstallBlockMetadata P1: outcome=%v cause=%v", installed.Outcome, installed.Cause)
	}
	t.Cleanup(func() {
		_ = blockStore.DeleteBlockByStorageKey(context.Background(), key)
	})
	return blockID, key
}

func x1CandidateAndQueue(t *testing.T, store *gcpkg.CassandraStore, orgID uuid.UUID, blockID, storageClass string, at time.Time) gcpkg.BlockGCCandidateInfo {
	t.Helper()
	at = at.UTC().Truncate(time.Millisecond)
	candidate, err := store.EnsureBlockGCCandidateExact(orgID, blockID, storageClass, at)
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact: %v", err)
	}
	queue := gcpkg.NewQueue(store)
	if err := queue.EnqueueBatch([]gcpkg.QueueItem{{
		OrgID:                    orgID,
		QueuedAt:                 candidate.CandidateAt,
		IdentityAt:               candidate.CandidateAt,
		ItemType:                 gcpkg.ItemBlock,
		ItemID:                   blockID,
		LibraryID:                uuid.Nil,
		StorageClass:             candidate.StorageClass(),
		BlockGCCandidateIdentity: candidate.Identity(),
	}}); err != nil {
		t.Fatalf("EnqueueBatch: %v", err)
	}
	return candidate
}

func x1CandidateListedOnDay(t *testing.T, store *gcpkg.CassandraStore, orgID uuid.UUID, blockID string, candidateAt time.Time) bool {
	t.Helper()
	listed, err := store.ListBlockGCCandidatesByDay(dbpkg.GCProjectionUTCDate(candidateAt), dbpkg.GCDiscoveryBucket(orgID.String(), blockID))
	if err != nil {
		t.Fatalf("ListBlockGCCandidatesByDay: %v", err)
	}
	for _, row := range listed {
		if row.OrgID == orgID && row.BlockID == blockID {
			return true
		}
	}
	return false
}

func x1Target(storageClass, storageKey string) gcpkg.BlockDeleteTarget {
	return gcpkg.BlockDeleteTarget{StorageClass: storageClass, StorageKey: storageKey}
}

func x1RegisterFenced(t *testing.T, database *dbpkg.DB, orgID uuid.UUID, blockID, operationID, storageClass, storageKey string) {
	t.Helper()
	err := v2pkg.NewFSHelper(database).RegisterUploadedBlockTarget(
		t.Context(), orgID.String(), uuid.NewString(), blockID, operationID, 1,
		v2pkg.BlockMaterializationTarget{StorageClass: storageClass, StorageKey: storageKey}, "",
	)
	if !errors.Is(err, v2pkg.ErrBlockDeleteInProgress) {
		t.Fatalf("RegisterUploadedBlockTarget = %v, want ErrBlockDeleteInProgress", err)
	}
}

func x1DeleteQueueRowKeepPending(t *testing.T, database *dbpkg.DB, orgID uuid.UUID, blockID string, candidate gcpkg.BlockGCCandidateInfo) {
	t.Helper()
	identity := candidate.ItemIdentity()
	err := database.Session().Query(`
		DELETE FROM gc_queue
		WHERE org_id = ? AND bucket = ? AND queued_at = ? AND item_type = ? AND item_id = ? AND candidate_storage_class = ? AND candidate_storage_key = ? AND identity_at = ?
	`, orgID.String(), gcpkg.QueueBucket(orgID, gcpkg.ItemBlock, blockID), candidate.CandidateAt, string(gcpkg.ItemBlock), blockID, identity.Target().StorageClass, identity.Target().StorageKey, identity.IdentityAt).Exec()
	if err != nil {
		t.Fatalf("delete exact gc_queue row: %v", err)
	}
}

func x1FailedItemExists(t *testing.T, store *gcpkg.CassandraStore, orgID uuid.UUID, blockID string) bool {
	t.Helper()
	items, err := store.ListFailedItems(orgID, 0)
	if err != nil {
		t.Fatalf("ListFailedItems: %v", err)
	}
	for _, item := range items {
		if item.ItemID == blockID && item.ItemType == gcpkg.ItemBlock {
			return true
		}
	}
	return false
}

func x1RejectForeignTenantDelete(t *testing.T, k1 string) {
	t.Helper()
	foreign := newVerificationBlockStore(t, uuid.New().String())
	err := foreign.DeleteBlockByStorageKey(t.Context(), k1)
	if err == nil {
		t.Fatal("E: foreign-tenant DeleteBlockByStorageKey must fail before S3")
	}
	if !strings.Contains(err.Error(), "outside tenant prefix") {
		t.Fatalf("E: want tenant-prefix rejection, got %v", err)
	}
}

func x1HasFSReferrer(t *testing.T, database *dbpkg.DB, orgID uuid.UUID, blockID string) bool {
	t.Helper()
	referrers, err := database.ListBlockReferrers(orgID.String(), blockID)
	if err != nil {
		t.Fatalf("ListBlockReferrers: %v", err)
	}
	for _, referrer := range referrers {
		if strings.HasPrefix(referrer, "fs:") {
			return true
		}
	}
	return false
}

func x1ScanOrphanedBlocksWithRestoredCursor(t *testing.T, store *gcpkg.CassandraStore, cursorDay time.Time) int {
	t.Helper()
	key := gcpkg.BlockCandidatesScanCursorKey
	prev, loadErr := store.LoadGCStats(key)
	hadPrev := false
	if loadErr == nil {
		hadPrev = true
	} else if !errors.Is(loadErr, gocql.ErrNotFound) {
		t.Fatalf("LoadGCStats(%s): %v", key, loadErr)
	}
	database := shareProjectionDBForTest(t)
	t.Cleanup(func() {
		if hadPrev {
			_ = store.SaveGCStats(key, prev)
			return
		}
		_ = database.Session().Query(`DELETE FROM gc_stats WHERE stat_key = ?`, key).Exec()
	})
	if err := store.SaveGCStats(key, dbpkg.GCProjectionDateString(cursorDay)); err != nil {
		t.Fatalf("save block-candidates cursor: %v", err)
	}
	scanner := gcpkg.NewScanner(store, gcpkg.NewQueue(store), &gcpkg.Stats{}, config.GCConfig{})
	n, err := scanner.ScanOrphanedBlocksOnce(t.Context())
	if err != nil {
		t.Fatalf("ScanOrphanedBlocksOnce: %v", err)
	}
	return n
}
