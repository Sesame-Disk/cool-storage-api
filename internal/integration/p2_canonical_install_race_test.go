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
	"github.com/google/uuid"
)

// TestP2ConcurrentCanonicalInstall proves the R9/R24 collision contract against
// real Cassandra and MinIO: contenders own distinct physical incarnations, one
// complete tuple wins, and cleanup can remove only the known loser.
func TestP2ConcurrentCanonicalInstall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	database := shareProjectionDBForTest(t)
	orgID := uuid.NewString()
	content := []byte(fmt.Sprintf("p2-install-race-%d", time.Now().UnixNano()))
	digest := sha256.Sum256(content)
	blockID := hex.EncodeToString(digest[:])
	storageClass := discoverStorageClass(t)
	blockStore := newVerificationBlockStore(t, orgID)

	key1, err := blockStore.MintStorageKey(blockID)
	if err != nil {
		t.Fatalf("mint W1 locator: %v", err)
	}
	key2, err := blockStore.MintStorageKey(blockID)
	if err != nil {
		t.Fatalf("mint W2 locator: %v", err)
	}
	if key1 == key2 {
		t.Fatalf("contenders minted the same locator %q", key1)
	}
	for _, key := range []string{key1, key2} {
		if _, err := blockStore.PutObjectAutoDirect(ctx, key, content); err != nil {
			t.Fatalf("PUT candidate %q: %v", key, err)
		}
	}
	t.Cleanup(func() {
		if err := database.Session().Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Exec(); err != nil {
			t.Errorf("cleanup blocks row: %v", err)
		}
		for _, key := range []string{key1, key2} {
			if err := blockStore.DeleteBlockByStorageKey(context.Background(), key); err != nil {
				t.Errorf("cleanup object %q: %v", key, err)
			}
		}
	})

	type attempt struct {
		proposed db.BlockPhysicalLocation
		result   db.InstallBlockMetadataResult
	}
	ready := make(chan db.BlockPhysicalLocation, 2)
	start := make(chan struct{})
	results := make(chan attempt, 2)
	for _, key := range []string{key1, key2} {
		proposed := db.BlockPhysicalLocation{StorageClass: storageClass, StorageKey: key}
		go func() {
			ready <- proposed
			<-start
			results <- attempt{
				proposed: proposed,
				result: database.InstallBlockMetadata(
					ctx, orgID, db.PlainBlockRepresentationID, blockID, "", len(content), proposed,
				),
			}
		}()
	}
	contenders := []db.BlockPhysicalLocation{<-ready, <-ready}
	if contenders[0] == contenders[1] || contenders[0].StorageKey == contenders[1].StorageKey {
		t.Fatalf("contention gate received non-distinct candidates: %+v", contenders)
	}
	t.Logf("P2_CONTENTION_EVIDENCE candidates=%d keys=%q,%q", len(contenders), contenders[0].StorageKey, contenders[1].StorageKey)
	close(start)
	attempts := []attempt{<-results, <-results}

	var winner, loser attempt
	applied, knownLost := 0, 0
	for _, contender := range attempts {
		switch contender.result.Outcome {
		case db.InstallBlockMetadataApplied:
			applied++
			winner = contender
		case db.InstallBlockMetadataKnownLost:
			knownLost++
			loser = contender
		default:
			t.Fatalf("install for %q remained ambiguous: %v", contender.proposed.StorageKey, contender.result.Cause)
		}
	}
	if applied != 1 || knownLost != 1 {
		t.Fatalf("outcomes: applied=%d known_lost=%d, want one each (%+v)", applied, knownLost, attempts)
	}
	if winner.result.Canonical != winner.proposed || loser.result.Canonical != winner.proposed {
		t.Fatalf("tuple agreement failed: winner=%+v loser=%+v", winner, loser)
	}

	var persistedClass, persistedKey string
	if err := database.Session().Query(
		`SELECT storage_class, storage_key FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID,
	).Scan(&persistedClass, &persistedKey); err != nil {
		t.Fatalf("read canonical row: %v", err)
	}
	if persistedClass != winner.proposed.StorageClass || persistedKey != winner.proposed.StorageKey {
		t.Fatalf("persisted tuple = (%q, %q), winner = %+v", persistedClass, persistedKey, winner.proposed)
	}
	var rowCount int64
	if err := database.Session().Query(
		`SELECT COUNT(*) FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count canonical rows: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("canonical row count = %d, want 1", rowCount)
	}

	if err := blockStore.DeleteBlockByStorageKey(ctx, loser.proposed.StorageKey); err != nil {
		t.Fatalf("delete known loser %q: %v", loser.proposed.StorageKey, err)
	}
	loserExists, err := blockStore.ObjectExists(ctx, loser.proposed.StorageKey)
	if err != nil {
		t.Fatalf("HEAD loser: %v", err)
	}
	if loserExists {
		t.Fatalf("known loser %q survived exact cleanup", loser.proposed.StorageKey)
	}
	winnerBytes, err := blockStore.GetBlockByStorageKey(ctx, winner.proposed.StorageKey)
	if err != nil {
		t.Fatalf("GET winner %q after loser cleanup: %v", winner.proposed.StorageKey, err)
	}
	if !bytes.Equal(winnerBytes, content) {
		t.Fatalf("winner bytes changed after loser cleanup: got %q want %q", winnerBytes, content)
	}
}
