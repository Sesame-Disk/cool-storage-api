//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/google/uuid"
)

// TestP2ConcurrentCanonicalInstall proves the R9/R24 collision contract against
// real Cassandra and MinIO: contenders own distinct physical incarnations, one
// complete tuple wins, and cleanup can remove only the known loser.
func TestP2ConcurrentCanonicalInstall(t *testing.T) {
	// MUST be first: the gate can only convert a skip into a failure if it is
	// registered before any helper that is able to skip.
	evidence := p2RequireContentionEvidence(t)

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
	evidence.record(len(contenders))
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

// p2RequireEvidenceEnv turns TestP2ConcurrentCanonicalInstall's skips into
// failures. P2/R9/R24 are documented as CLOSED on the strength of this one real
// Cassandra+MinIO race, so the command that produces that evidence must not be
// able to report success without it.
//
// It can: this test reaches t.Skip through its environment helpers -- an
// unreachable MinIO store or bucket (newVerificationS3Store), an empty
// storage_class, or a verification BlockStore pointed at a different bucket
// (discoverStorageClass). A skipped Go test exits 0, so a bare `go test` here
// prints "PASS / ok" and proves nothing. An earlier revision of this branch
// guarded the command with greps for "--- PASS" and "P2_CONTENTION_EVIDENCE"
// and against "--- SKIP"; that guard was dropped and this replaces it, in the
// test itself rather than in shell the host has to quote correctly.
const p2RequireEvidenceEnv = "SESAMEFS_REQUIRE_P2_EVIDENCE"

// p2EvidenceGate records that the real two-candidate contention actually happened.
type p2EvidenceGate struct{ candidates int }

func (gate *p2EvidenceGate) record(candidates int) { gate.candidates = candidates }

// p2RequireContentionEvidence arms the gate when p2RequireEvidenceEnv is set. The
// check runs in a cleanup, which still executes after t.Skip and whose failure
// overrides the skip -- so a skip anywhere in the helper chain becomes a FAIL and
// a non-zero exit code. Unset, the test keeps its normal skip-on-missing-env
// behavior for developers running the suite without a full stack.
func p2RequireContentionEvidence(t *testing.T) *p2EvidenceGate {
	t.Helper()
	gate := &p2EvidenceGate{}
	if os.Getenv(p2RequireEvidenceEnv) != "1" {
		return gate
	}
	t.Cleanup(func() {
		if t.Skipped() {
			t.Errorf("%s=1 requires this test to run, but it skipped: the P2/R9/R24 closure evidence was not produced. Bring up Cassandra and MinIO (docker compose up -d) and re-run.", p2RequireEvidenceEnv)
			return
		}
		if t.Failed() {
			return
		}
		if gate.candidates != 2 {
			t.Errorf("%s=1 requires P2_CONTENTION_EVIDENCE candidates=2, got candidates=%d: the run passed without two real contending incarnations.", p2RequireEvidenceEnv, gate.candidates)
		}
	})
	return gate
}
