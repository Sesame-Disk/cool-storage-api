//go:build integration

package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	v2pkg "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/google/uuid"
)

const r3CharacterizationEvidenceEnv = "SESAMEFS_REQUIRE_R3_CHARACTERIZATION"

type r3CharacterizationEvidenceGate struct{ observed bool }

func r3RequireCharacterizationEvidence(t *testing.T) *r3CharacterizationEvidenceGate {
	t.Helper()
	gate := &r3CharacterizationEvidenceGate{}
	if os.Getenv(r3CharacterizationEvidenceEnv) != "1" {
		return gate
	}
	t.Cleanup(func() {
		if t.Skipped() {
			t.Errorf("%s=1 requires real Cassandra R3 evidence, but the test skipped", r3CharacterizationEvidenceEnv)
		} else if !t.Failed() && !gate.observed {
			t.Errorf("%s=1 completed without R3 liveness evidence", r3CharacterizationEvidenceEnv)
		}
	})
	return gate
}

func r3TestBlockID(seed string) string {
	digest := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(digest[:])
}

func cleanupR3LivenessFixture(t *testing.T, database *dbpkg.DB, orgID uuid.UUID, blockID, referrer string) {
	t.Helper()
	t.Cleanup(func() {
		if err := database.DeleteProvisionalBlockReferenceExpiry(orgID.String(), blockID, referrer, time.Time{}); err != nil {
			t.Errorf("cleanup provisional expiry %s/%s: %v", orgID, blockID, err)
		}
		if err := database.Session().Query(`DELETE FROM block_references WHERE org_id = ? AND block_id = ?`, orgID.String(), blockID).Exec(); err != nil {
			t.Errorf("cleanup block references %s/%s: %v", orgID, blockID, err)
		}
		cleanupGCBlockFixturesForTest(t, orgID, blockID)
		if err := database.Session().Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID.String(), blockID).Exec(); err != nil {
			t.Errorf("cleanup canonical block %s/%s: %v", orgID, blockID, err)
		}
	})
}

func addR3UploadPin(t *testing.T, database *dbpkg.DB, orgID uuid.UUID, blockID, referrer string) {
	t.Helper()
	if err := database.AddProvisionalBlockReferenceWithExpiry(
		orgID.String(), blockID, referrer, uuid.NewString(), "hot",
		time.Now().UTC().Add(time.Duration(dbpkg.ProvisionalBlockReferenceTTLSeconds)*time.Second),
	); err != nil {
		t.Fatalf("write R3 provisional pin %s/%s: %v", orgID, blockID, err)
	}
}

// TestR3WriterGCHandshakeAtRealCassandra stops before physical deletion. The
// evidence sought here is whether GC's post-claim liveness read or the writer's
// post-pin fence forces one side to lose authority.
func TestR3WriterGCHandshakeAtRealCassandra(t *testing.T) {
	requireCassandra(t)
	gate := r3RequireCharacterizationEvidence(t)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)

	t.Run("writer pin wins before global liveness verify", func(t *testing.T) {
		orgID := uuid.New()
		blockID := r3TestBlockID("r3-writer-wins-" + uuid.NewString())
		target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
		referrer := dbpkg.BlockReferrerForUpload("r3-writer-" + uuid.NewString())
		cleanupR3LivenessFixture(t, database, orgID, blockID, referrer)

		addR3UploadPin(t, database, orgID, blockID, referrer)
		attempt := gcpkg.BlockDeleteAuthority{Target: target, ClaimID: "r3-gc-" + uuid.NewString(), ClaimedAt: time.Now().UTC().Truncate(time.Millisecond)}
		claim, err := store.ClaimBlockDelete(orgID, blockID, attempt)
		if err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
			t.Fatalf("GC claim after writer pin = %s, %v; want acquired before verify", claim.Outcome, err)
		}
		hasRefs, err := store.BlockHasReferencesGlobal(orgID, blockID)
		if err != nil {
			t.Fatalf("global liveness verify after writer pin: %v", err)
		}
		if !hasRefs {
			t.Fatal("R3 WRITER-WINS EVIDENCE: EACH_QUORUM global verify missed the acknowledged upload pin")
		}
		released, err := store.ReleaseBlockClaim(orgID, blockID, attempt)
		if err != nil || released != gcpkg.BlockReleaseReleased {
			t.Fatalf("release GC claim after positive liveness = %s, %v; want released", released, err)
		}
		fenced, err := database.BlockDeleteFenceActive(orgID.String(), blockID)
		if err != nil || fenced {
			t.Fatalf("writer fence after GC release = %v, %v; want false, nil", fenced, err)
		}
	})

	t.Run("GC claim wins and deleting row fences writer", func(t *testing.T) {
		orgID := uuid.New()
		blockID := r3TestBlockID("r3-gc-deleting-" + uuid.NewString())
		target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
		operationID := "r3-writer-" + uuid.NewString()
		referrer := dbpkg.BlockReferrerForUpload(operationID)
		cleanupR3LivenessFixture(t, database, orgID, blockID, referrer)

		attempt := gcpkg.BlockDeleteAuthority{Target: target, ClaimID: "r3-gc-" + uuid.NewString(), ClaimedAt: time.Now().UTC().Truncate(time.Millisecond)}
		claim, err := store.ClaimBlockDelete(orgID, blockID, attempt)
		if err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
			t.Fatalf("GC claim = %s, %v; want acquired", claim.Outcome, err)
		}
		if hasRefs, err := store.BlockHasReferencesGlobal(orgID, blockID); err != nil || hasRefs {
			t.Fatalf("GC global liveness before writer pin = %v, %v; want false, nil", hasRefs, err)
		}

		addR3UploadPin(t, database, orgID, blockID, referrer)
		err = v2pkg.NewFSHelper(database).RegisterUploadedBlockTarget(
			t.Context(), orgID.String(), uuid.NewString(), blockID, operationID, 1,
			v2pkg.BlockMaterializationTarget{StorageClass: target.StorageClass, StorageKey: target.StorageKey}, "",
		)
		if !errors.Is(err, v2pkg.ErrBlockDeleteInProgress) {
			t.Fatalf("R3 GC-WINS DELETING EVIDENCE: materialization error = %v, want ErrBlockDeleteInProgress", err)
		}
		if released, releaseErr := store.ReleaseBlockClaim(orgID, blockID, attempt); releaseErr != nil || released != gcpkg.BlockReleaseReleased {
			t.Fatalf("cleanup release GC claim = %s, %v", released, releaseErr)
		}
	})

	t.Run("GC orphan fences writer after irreversible handoff", func(t *testing.T) {
		orgID := uuid.New()
		blockID := r3TestBlockID("r3-gc-orphan-" + uuid.NewString())
		target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
		operationID := "r3-writer-" + uuid.NewString()
		referrer := dbpkg.BlockReferrerForUpload(operationID)
		cleanupR3LivenessFixture(t, database, orgID, blockID, referrer)

		attempt := gcpkg.BlockDeleteAuthority{Target: target, ClaimID: "r3-gc-" + uuid.NewString(), ClaimedAt: time.Now().UTC().Truncate(time.Millisecond)}
		claim, err := store.ClaimBlockDelete(orgID, blockID, attempt)
		if err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
			t.Fatalf("GC claim = %s, %v; want acquired", claim.Outcome, err)
		}
		if hasRefs, err := store.BlockHasReferencesGlobal(orgID, blockID); err != nil || hasRefs {
			t.Fatalf("GC global liveness before writer pin = %v, %v; want false, nil", hasRefs, err)
		}
		handoff, err := store.CommitBlockDeleteOrphanHandoff(orgID, blockID, attempt)
		if err != nil || (handoff.Outcome != gcpkg.BlockDeleteHandoffCommitted && handoff.Outcome != gcpkg.BlockDeleteHandoffAlreadyCommitted) {
			t.Fatalf("commit orphan handoff = %s, %v; want committed", handoff.Outcome, err)
		}
		published := store.StartBlockDeleteOrphan(
			orgID, blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(attempt), strings.Repeat("a", 40),
			time.Now().UTC().Truncate(time.Millisecond),
		)
		if published.Outcome != gcpkg.StartBlockDeleteOrphanCreated && published.Outcome != gcpkg.StartBlockDeleteOrphanSameAuthority {
			t.Fatalf("publish orphan fence = %s cause=%v", published.Outcome, published.Cause)
		}
		t.Cleanup(func() {
			if err := store.DeleteS3Orphan(orgID, blockID, published.FirstSeenAt); err != nil {
				t.Logf("cleanup R3 orphan: %v", err)
			}
		})

		addR3UploadPin(t, database, orgID, blockID, referrer)
		err = v2pkg.NewFSHelper(database).RegisterUploadedBlockTarget(
			t.Context(), orgID.String(), uuid.NewString(), blockID, operationID, 1,
			v2pkg.BlockMaterializationTarget{StorageClass: target.StorageClass, StorageKey: target.StorageKey}, "",
		)
		if !errors.Is(err, v2pkg.ErrBlockDeleteInProgress) {
			t.Fatalf("R3 GC-WINS ORPHAN EVIDENCE: materialization error = %v, want ErrBlockDeleteInProgress", err)
		}
	})

	gate.observed = true
	t.Log("R3_LIVENESS_CHARACTERIZATION_EVIDENCE writer_wins=1 deleting_fence=1 orphan_fence=1")
}

func TestR3CharacterizationBaseIsDocumented(t *testing.T) {
	raw, err := os.ReadFile("../../docs/R3-LIVENESS-CONTINUITY.md")
	if err != nil {
		t.Fatalf("read R3 characterization document: %v", err)
	}
	text := string(raw)
	for _, required := range []string{"c0da425a4", "R3 remains OPEN", "GC_ENABLED=false", fmt.Sprintf("`%s=1`", r3CharacterizationEvidenceEnv)} {
		if !strings.Contains(text, required) {
			t.Fatalf("R3 characterization document is missing %q", required)
		}
	}
}
