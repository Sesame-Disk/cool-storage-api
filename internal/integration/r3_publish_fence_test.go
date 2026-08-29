//go:build integration

package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/google/uuid"
)

const r3RequireEvidenceEnv = "SESAMEFS_REQUIRE_R3_EVIDENCE"

type r3EvidenceGate struct{ observed bool }

func r3RequireEvidence(t *testing.T) *r3EvidenceGate {
	t.Helper()
	gate := &r3EvidenceGate{}
	if os.Getenv(r3RequireEvidenceEnv) != "1" {
		return gate
	}
	t.Cleanup(func() {
		if t.Skipped() {
			t.Errorf("%s=1 requires real Cassandra R3 evidence, but the test skipped", r3RequireEvidenceEnv)
		} else if !t.Failed() && !gate.observed {
			t.Errorf("%s=1 completed without R3 evidence", r3RequireEvidenceEnv)
		}
	})
	return gate
}

func r3TestBlockID(seed string) string {
	sum := sha256.Sum256([]byte(seed + uuid.NewString()))
	return hex.EncodeToString(sum[:])
}

func r3CleanupBlock(t *testing.T, database *dbpkg.DB, orgID uuid.UUID, blockID string) {
	t.Helper()
	t.Cleanup(func() {
		// DeleteS3Orphan reads first_seen_at from the canonical row, then
		// removes both gc_s3_orphans and gc_s3_orphans_by_day. A raw DELETE of
		// the canonical row first leaves the discovery index behind and the
		// local scanner reports "canonical S3 orphan missing".
		_ = gcpkg.NewCassandraStore(database).DeleteS3Orphan(orgID, blockID, time.Time{})
		_ = database.Session().Query(`DELETE FROM block_references WHERE org_id = ? AND block_id = ?`, orgID.String(), blockID).Exec()
		_ = database.Session().Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID.String(), blockID).Exec()
	})
}

func r3HasAttemptRef(t *testing.T, database *dbpkg.DB, orgID uuid.UUID, blockID, attemptID string) bool {
	t.Helper()
	exists, err := database.BlockReferenceExists(orgID.String(), blockID, dbpkg.BlockReferrerForPublishAttempt(attemptID))
	if err != nil {
		t.Fatalf("BlockReferenceExists(%s): %v", attemptID, err)
	}
	return exists
}

func TestR3_ActiveBlockMayStagePublishAttempt(t *testing.T) {
	requireCassandra(t)
	gate := r3RequireEvidence(t)

	database := shareProjectionDBForTest(t)
	orgID := uuid.New()
	repoID := uuid.NewString()
	blockID := r3TestBlockID("r3-active")
	r3CleanupBlock(t, database, orgID, blockID)
	seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")

	attemptID := "r3-active-" + uuid.NewString()
	staged, err := dbpkg.StagePublishAttemptReferences(database, orgID.String(), repoID, attemptID, []string{blockID}, nil)
	if err != nil {
		t.Fatalf("StagePublishAttemptReferences on an Active block = %v, want nil", err)
	}
	if len(staged) != 1 || staged[0] != blockID {
		t.Fatalf("staged = %#v, want []string{%q}", staged, blockID)
	}
	if !r3HasAttemptRef(t, database, orgID, blockID, attemptID) {
		t.Fatal("Active stage must leave this attempt's pub: row")
	}
	gate.observed = true
	t.Logf("R3_EVIDENCE active org=%s block=%s attempt=%s", orgID, blockID, attemptID)
}

func TestR3_ClaimedBlockCannotStagePublishAttempt(t *testing.T) {
	requireCassandra(t)
	gate := r3RequireEvidence(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	repoID := uuid.NewString()
	blockID := r3TestBlockID("r3-claim")
	r3CleanupBlock(t, database, orgID, blockID)
	target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
	authority := gcpkg.BlockDeleteAuthority{
		Target:    target,
		ClaimID:   "r3-claim-" + uuid.NewString(),
		ClaimedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	claim, err := store.ClaimBlockDelete(orgID, blockID, authority)
	if err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("claim = %s, %v; want acquired", claim.Outcome, err)
	}

	hasRefs, err := store.BlockHasReferencesGlobal(orgID, blockID)
	if err != nil {
		t.Fatalf("BlockHasReferencesGlobal: %v", err)
	}
	if hasRefs {
		t.Fatal("fixture is dirty: refs exist before the writer stages")
	}

	attemptID := "r3-claim-attempt-" + uuid.NewString()
	staged, err := dbpkg.StagePublishAttemptReferences(database, orgID.String(), repoID, attemptID, []string{blockID}, nil)
	if staged != nil {
		t.Fatalf("denied stage returned %#v, want nil", staged)
	}
	if !errors.Is(err, dbpkg.ErrBlockPublishAuthorityDenied) {
		t.Fatalf("stage after claim = %v, want ErrBlockPublishAuthorityDenied", err)
	}
	outcome, checkErr := dbpkg.ValidatePublishAttemptAuthority(database, orgID.String(), []string{blockID})
	if outcome != dbpkg.BlockPublishAuthorityDeleting {
		t.Fatalf("Validate after claim = %v, %v; want deleting", outcome, checkErr)
	}
	if r3HasAttemptRef(t, database, orgID, blockID, attemptID) {
		t.Fatal("denied stage must roll back this attempt's pub: rows")
	}
	gate.observed = true
	t.Logf("R3_EVIDENCE deleting org=%s block=%s", orgID, blockID)
}

func TestR3_RollbackIsScopedToThisAttempt(t *testing.T) {
	requireCassandra(t)
	gate := r3RequireEvidence(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	repoID := uuid.NewString()
	blockID := r3TestBlockID("r3-scope")
	r3CleanupBlock(t, database, orgID, blockID)
	target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")

	keepID := "r3-keep-" + uuid.NewString()
	if _, err := dbpkg.StagePublishAttemptReferences(database, orgID.String(), repoID, keepID, []string{blockID}, nil); err != nil {
		t.Fatalf("first Active stage: %v", err)
	}

	authority := gcpkg.BlockDeleteAuthority{
		Target:    target,
		ClaimID:   "r3-scope-" + uuid.NewString(),
		ClaimedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	claim, err := store.ClaimBlockDelete(orgID, blockID, authority)
	if err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("claim = %s, %v; want acquired", claim.Outcome, err)
	}

	dropID := "r3-drop-" + uuid.NewString()
	_, err = dbpkg.StagePublishAttemptReferences(database, orgID.String(), repoID, dropID, []string{blockID}, nil)
	if !errors.Is(err, dbpkg.ErrBlockPublishAuthorityDenied) {
		t.Fatalf("second stage after claim = %v, want denied", err)
	}
	if r3HasAttemptRef(t, database, orgID, blockID, dropID) {
		t.Fatal("denied attempt's pub: rows must be gone")
	}
	if !r3HasAttemptRef(t, database, orgID, blockID, keepID) {
		t.Fatal("rollback without this attempt's id retracted another publisher's pub:")
	}
	gate.observed = true
	t.Logf("R3_EVIDENCE attempt-scoped rollback org=%s block=%s keep=%s drop=%s", orgID, blockID, keepID, dropID)
}

func TestR3_HandoffAndOrphanCannotStagePublishAttempt(t *testing.T) {
	requireCassandra(t)
	gate := r3RequireEvidence(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	repoID := uuid.NewString()
	blockID := r3TestBlockID("r3-handoff")
	r3CleanupBlock(t, database, orgID, blockID)
	target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
	authority := gcpkg.BlockDeleteAuthority{
		Target:    target,
		ClaimID:   "r3-handoff-" + uuid.NewString(),
		ClaimedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	claim, err := store.ClaimBlockDelete(orgID, blockID, authority)
	if err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("claim = %s, %v; want acquired", claim.Outcome, err)
	}
	handoff, err := store.CommitBlockDeleteOrphanHandoff(orgID, blockID, authority)
	if err != nil || (handoff.Outcome != gcpkg.BlockDeleteHandoffCommitted && handoff.Outcome != gcpkg.BlockDeleteHandoffAlreadyCommitted) {
		t.Fatalf("handoff = %s, %v; want committed", handoff.Outcome, err)
	}

	outcome, checkErr := dbpkg.ValidatePublishAttemptAuthority(database, orgID.String(), []string{blockID})
	if outcome != dbpkg.BlockPublishAuthorityDeleting {
		t.Fatalf("Validate after handoff = %v, %v; ignoring handoff must not be Active", outcome, checkErr)
	}

	committed := gcpkg.CommittedBlockDeleteAuthorityForTest(authority)
	created := store.StartBlockDeleteOrphan(orgID, blockID, committed, "", time.Now().UTC().Truncate(time.Millisecond))
	if created.Outcome != gcpkg.StartBlockDeleteOrphanCreated && created.Outcome != gcpkg.StartBlockDeleteOrphanSameAuthority {
		t.Fatalf("publish orphan = %s cause=%v, want created", created.Outcome, created.Cause)
	}

	attemptID := "r3-orphan-attempt-" + uuid.NewString()
	_, err = dbpkg.StagePublishAttemptReferences(database, orgID.String(), repoID, attemptID, []string{blockID}, nil)
	if !errors.Is(err, dbpkg.ErrBlockPublishAuthorityDenied) {
		t.Fatalf("stage after orphan = %v, want denied", err)
	}
	if r3HasAttemptRef(t, database, orgID, blockID, attemptID) {
		t.Fatal("denied orphan stage must roll back this attempt's pub:")
	}

	if _, err := store.FinalizeBlockDelete(orgID, blockID, committed); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	outcome, checkErr = dbpkg.ValidatePublishAttemptAuthority(database, orgID.String(), []string{blockID})
	if outcome != dbpkg.BlockPublishAuthorityOrphaned {
		t.Fatalf("Validate after finalize = %v, %v; want orphaned (rowless + orphan fence)", outcome, checkErr)
	}
	gate.observed = true
	t.Logf("R3_EVIDENCE handoff/orphan org=%s block=%s", orgID, blockID)
}

func TestR3_RepairingStubMissingAndInvalidAreDenied(t *testing.T) {
	requireCassandra(t)
	gate := r3RequireEvidence(t)

	database := shareProjectionDBForTest(t)
	orgID := uuid.New()

	missingID := r3TestBlockID("r3-missing")
	outcome, err := dbpkg.ValidatePublishAttemptAuthority(database, orgID.String(), []string{missingID})
	if outcome != dbpkg.BlockPublishAuthorityMissing {
		t.Fatalf("missing canonical row = %v, %v; want missing", outcome, err)
	}

	repairID := r3TestBlockID("r3-repair")
	r3CleanupBlock(t, database, orgID, repairID)
	claimedAt := time.Now().UTC()
	// Production repairing_stub is a metadata-free upload claim
	// (created_at and storage_class null). Locator validation therefore
	// classifies it Invalid before Repairing; both are fail-closed.
	if err := database.Session().Query(`
		INSERT INTO blocks (org_id, block_id, gc_state, gc_claim_id, gc_claimed_at)
		VALUES (?, ?, ?, ?, ?)
	`, orgID.String(), repairID, dbpkg.BlockGCStateRepairingStub, "r3-repair-"+uuid.NewString(), claimedAt).Exec(); err != nil {
		t.Fatalf("insert metadata-free repairing_stub: %v", err)
	}
	outcome, err = dbpkg.ValidatePublishAttemptAuthority(database, orgID.String(), []string{repairID})
	if outcome == dbpkg.BlockPublishAuthorityActive {
		t.Fatalf("production-shaped repairing_stub = %v, %v; must not be Active", outcome, err)
	}
	if outcome != dbpkg.BlockPublishAuthorityRepairing && outcome != dbpkg.BlockPublishAuthorityInvalid {
		t.Fatalf("repairing_stub = %v, %v; want repairing or invalid (never Active)", outcome, err)
	}

	invalidID := r3TestBlockID("r3-invalid")
	r3CleanupBlock(t, database, orgID, invalidID)
	seedCanonicalBlockRowForTest(t, database, orgID, invalidID, "hot")
	if err := database.Session().Query(`
		UPDATE blocks SET storage_key = ? WHERE org_id = ? AND block_id = ?
	`, "", orgID.String(), invalidID).Exec(); err != nil {
		t.Fatalf("clear storage_key: %v", err)
	}
	outcome, err = dbpkg.ValidatePublishAttemptAuthority(database, orgID.String(), []string{invalidID})
	if outcome != dbpkg.BlockPublishAuthorityInvalid {
		t.Fatalf("empty storage_key = %v, %v; want invalid", outcome, err)
	}

	if outcome, err := dbpkg.ValidatePublishAttemptAuthority(database, orgID.String(), []string{"not-sha256"}); outcome != dbpkg.BlockPublishAuthorityInvalid || !errors.Is(err, dbpkg.ErrBlockPublishAuthorityDenied) {
		t.Fatalf("non-SHA-256 = %v, %v; want invalid", outcome, err)
	}

	gate.observed = true
	t.Logf("R3_EVIDENCE repairing/missing/invalid org=%s", orgID)
}

func TestR3_EmptyBatchIsVacuouslyActive(t *testing.T) {
	requireCassandra(t)
	gate := r3RequireEvidence(t)

	database := shareProjectionDBForTest(t)
	outcome, err := dbpkg.ValidatePublishAttemptAuthority(database, uuid.NewString(), nil)
	if err != nil || outcome != dbpkg.BlockPublishAuthorityActive {
		t.Fatalf("empty Validate = %v, %v; want active with zero reads", outcome, err)
	}
	staged, err := dbpkg.StagePublishAttemptReferences(database, uuid.NewString(), uuid.NewString(), uuid.NewString(), nil, nil)
	if err != nil {
		t.Fatalf("empty Stage = %v, want nil", err)
	}
	if len(staged) != 0 {
		t.Fatalf("staged = %#v, want empty", staged)
	}
	gate.observed = true
	t.Logf("R3_EVIDENCE empty-batch active")
}

func TestR3_WriterPubFirstMakesGCSeeReference(t *testing.T) {
	requireCassandra(t)
	gate := r3RequireEvidence(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	repoID := uuid.NewString()
	blockID := r3TestBlockID("r3-pub-first")
	r3CleanupBlock(t, database, orgID, blockID)
	target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")

	attemptID := "r3-pub-first-" + uuid.NewString()
	if err := dbpkg.AddPublishAttemptReferences(database, orgID.String(), repoID, attemptID, []string{blockID}); err != nil {
		t.Fatalf("AddPublishAttemptReferences: %v", err)
	}
	hasRefs, err := store.BlockHasReferencesGlobal(orgID, blockID)
	if err != nil {
		t.Fatalf("BlockHasReferencesGlobal after pub:: %v", err)
	}
	if !hasRefs {
		t.Fatal("R3 REGRESSION: GC verify missed this attempt's pub:; a post-stage LQ check cannot close the race if pub: is invisible")
	}

	authority := gcpkg.BlockDeleteAuthority{
		Target:    target,
		ClaimID:   "r3-pub-first-" + uuid.NewString(),
		ClaimedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	claim, err := store.ClaimBlockDelete(orgID, blockID, authority)
	if err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("claim after pub: = %s, %v; want acquired (refs are checked after claim)", claim.Outcome, err)
	}
	hasRefs, err = store.BlockHasReferencesGlobal(orgID, blockID)
	if err != nil {
		t.Fatalf("BlockHasReferencesGlobal after claim: %v", err)
	}
	if !hasRefs {
		t.Fatal("R3 REGRESSION: claim succeeded and then GC verify missed pub:; both sides would continue")
	}

	err = dbpkg.FinishCheckedPublishAttempt(database, orgID.String(), repoID, attemptID, []string{blockID})
	if !errors.Is(err, dbpkg.ErrBlockPublishAuthorityDenied) {
		t.Fatalf("FinishChecked after claim = %v, want denied", err)
	}
	if r3HasAttemptRef(t, database, orgID, blockID, attemptID) {
		t.Fatal("denied FinishChecked must roll back this attempt's pub:")
	}
	gate.observed = true
	t.Logf("R3_EVIDENCE pub-first org=%s block=%s attempt=%s", orgID, blockID, attemptID)
}

func TestR3_FenceReadConsistencyPinIsLocalQuorum(t *testing.T) {
	// The 3-DC fixture is RF=1/DC, so LOCAL_QUORUM ≡ ONE there. This pin is the
	// RF>1 half of the intersection argument and must stay in the same package
	// that docker-compose runs with SESAMEFS_REQUIRE_R3_EVIDENCE=1.
	if dbpkg.BlockFenceReadConsistency.String() != "LOCAL_QUORUM" {
		t.Fatalf("BlockFenceReadConsistency = %s, want LOCAL_QUORUM", dbpkg.BlockFenceReadConsistency)
	}
}
