package gc

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestP4B_InsertBlockDeleteLifecycleSourceContract(t *testing.T) {
	file := parseGCStoreFile(t)
	text := formattedGCFunction(t, file, "insertBlockDeleteLifecycle")
	if !strings.Contains(text, "INSERT INTO gc_block_delete_lifecycles") {
		t.Fatal("insertBlockDeleteLifecycle must INSERT the durable D tombstone")
	}
	if !strings.Contains(text, "IF NOT EXISTS") {
		t.Fatal("lifecycle INSERT must be write-once IF NOT EXISTS")
	}
	if !strings.Contains(text, "Consistency(gocql.EachQuorum)") {
		t.Fatal("lifecycle INSERT must pin regular consistency to EachQuorum")
	}
	if !strings.Contains(text, "SerialConsistency(gocql.Serial)") {
		t.Fatal("lifecycle INSERT must pin the LWT serial domain")
	}
	if !strings.Contains(text, "Idempotent(false)") || !strings.Contains(text, "NumRetries: 0") || !strings.Contains(text, "NonSpeculativeExecution") {
		t.Fatal("lifecycle INSERT must not hide an uncertain LWT behind driver retries")
	}
	if !strings.Contains(text, "len(existing) == 0") || !strings.Contains(text, "settleBlockDeleteLifecycle") {
		t.Fatal("empty non-applied lifecycle CAS must SERIAL-settle")
	}

	terminate := formattedGCFunction(t, file, "TerminateBlockDeleteLifecycle")
	if !strings.Contains(terminate, "UPDATE gc_block_delete_lifecycles SET phase") {
		t.Fatal("TerminateBlockDeleteLifecycle must CAS published → terminal")
	}
	if !strings.Contains(terminate, "IF phase = ?") {
		t.Fatal("lifecycle terminate must condition on the published phase")
	}
}

func TestP4B_LifecycleTableIsNotFoldedIntoInitialSchema(t *testing.T) {
	source, err := os.ReadFile("../db/migrations/001_initial_schema.cql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "gc_block_delete_lifecycles") {
		t.Fatal("do not fold gc_block_delete_lifecycles into 001_initial_schema.cql; X1 is still OPEN")
	}
	body, err := os.ReadFile("../db/migrations/020_gc_block_delete_lifecycle.cql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "CREATE TABLE IF NOT EXISTS gc_block_delete_lifecycles") {
		t.Fatal("migration 020 must create gc_block_delete_lifecycles")
	}
}

func TestP4B_CommitHandoffConfirmsAlreadyCommittedAtEachQuorum(t *testing.T) {
	file := parseGCStoreFile(t)
	confirm := formattedGCFunction(t, file, "confirmAlreadyCommittedHandoffEachQuorum")
	if !strings.Contains(confirm, "confirmCommittedBlockDeleteAuthorityEachQuorum") {
		t.Fatal("AlreadyCommitted must re-read the committed authority at EACH_QUORUM")
	}
	if !strings.Contains(confirm, "BlockDeleteHandoffAmbiguous") {
		t.Fatal("EACH_QUORUM miss after SERIAL AlreadyCommitted must be Ambiguous")
	}

	owner := formattedGCFunction(t, file, "confirmCommittedOwnerEachQuorum")
	if !strings.Contains(owner, "confirmCommittedBlockDeleteAuthorityEachQuorum") {
		t.Fatal("CommittedOwner must re-read the committed authority at EACH_QUORUM")
	}
	if !strings.Contains(owner, "BlockClaimAmbiguous") {
		t.Fatal("EACH_QUORUM miss after SERIAL CommittedOwner must be Ambiguous")
	}
}

func TestP4B_ProcessBlockCommittedOwnerRechecksRefs(t *testing.T) {
	source, err := os.ReadFile("worker.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "worker.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	text := formattedGCFunction(t, file, "processBlock")
	if strings.Count(text, "BlockHasReferencesGlobal") < 2 {
		t.Fatal("CommittedOwner must re-check BlockHasReferencesGlobal as a contradiction detector after locator/store/topology")
	}
	if !strings.Contains(text, "committed delete authority observed references after handoff") {
		t.Fatal("CommittedOwner refs>0 must refuse as committed_pending, not complete the delete")
	}

	helper := formattedGCFunction(t, file, "terminateThenDeleteS3Orphan")
	if !strings.Contains(helper, "TerminateBlockDeleteLifecycle") {
		t.Fatal("production orphan clear must CAS the lifecycle tombstone to terminal first")
	}
	if strings.Index(helper, "TerminateBlockDeleteLifecycle") > strings.Index(helper, "store.DeleteS3Orphan") {
		t.Fatal("terminal CAS must run before DeleteS3Orphan")
	}
	if strings.Count(string(source), "w.store.DeleteS3Orphan") != 1 {
		t.Fatal("production DeleteS3Orphan must only run from terminateThenDeleteS3Orphan")
	}
}

func TestP4B_AlreadyCommittedHandoffFailsClosedWhenEachQuorumUnconfirmed(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	blockID := "blk-handoff-eq"
	store.AddBlock(orgID, blockID, "hot", 0)
	owner := store.SeedBlockClaimForTest(orgID, blockID, "d1", time.Now().UTC())
	store.SeedBlockHandoffForTest(orgID, blockID)
	store.SetClaimBlockDeleteEachQuorumErrForTest(errors.New("Cannot achieve consistency level EACH_QUORUM in DC dc-na"))

	result, err := store.CommitBlockDeleteOrphanHandoff(orgID, blockID, owner)
	if result.Outcome == BlockDeleteHandoffAlreadyCommitted || result.Outcome == BlockDeleteHandoffCommitted {
		t.Fatal("AlreadyCommitted without EACH_QUORUM visibility must not be treated as durable authority")
	}
	if result.Outcome != BlockDeleteHandoffAmbiguous || err == nil {
		t.Fatalf("handoff = %s err=%v, want ambiguous with error", result.Outcome, err)
	}
}

func TestP4B_AlreadyCommittedHandoffSucceedsWhenEachQuorumConfirms(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	blockID := "blk-handoff-eq-ok"
	store.AddBlock(orgID, blockID, "hot", 0)
	owner := store.SeedBlockClaimForTest(orgID, blockID, "d1", time.Now().UTC())
	store.SeedBlockHandoffForTest(orgID, blockID)

	result, err := store.CommitBlockDeleteOrphanHandoff(orgID, blockID, owner)
	if err != nil || result.Outcome != BlockDeleteHandoffAlreadyCommitted {
		t.Fatalf("visible committed handoff = %s, %v; want already_committed", result.Outcome, err)
	}
	if result.Authority.Authority().ClaimID != owner.ClaimID {
		t.Fatalf("AlreadyCommitted resumed %q, want %q", result.Authority.Authority().ClaimID, owner.ClaimID)
	}
}

func TestP4B_CommittedOwnerFailsClosedWhenEachQuorumUnconfirmed(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	blockID := "blk-owner-eq"
	store.AddBlock(orgID, blockID, "hot", 0)
	store.SeedBlockClaimForTest(orgID, blockID, "d1", time.Now().UTC())
	store.SeedBlockHandoffForTest(orgID, blockID)
	store.SetClaimBlockDeleteEachQuorumErrForTest(errors.New("Cannot achieve consistency level EACH_QUORUM in DC dc-na"))

	fresh := store.BlockDeleteAuthorityForTest(orgID, blockID, "d2", time.Now().UTC())
	claim, err := store.ClaimBlockDelete(orgID, blockID, fresh)
	if claim.Outcome == BlockClaimCommittedOwner {
		t.Fatal("CAS-map CommittedOwner without EACH_QUORUM visibility must not be success")
	}
	if claim.Outcome != BlockClaimAmbiguous || err == nil {
		t.Fatalf("claim = %s err=%v, want ambiguous with error", claim.Outcome, err)
	}
}

func TestP4B_CommittedOwnerSucceedsWhenEachQuorumConfirms(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	blockID := "blk-owner-eq-ok"
	store.AddBlock(orgID, blockID, "hot", 0)
	store.SeedBlockClaimForTest(orgID, blockID, "d1", time.Now().UTC())
	store.SeedBlockHandoffForTest(orgID, blockID)

	fresh := store.BlockDeleteAuthorityForTest(orgID, blockID, "d2", time.Now().UTC())
	claim, err := store.ClaimBlockDelete(orgID, blockID, fresh)
	if err != nil || claim.Outcome != BlockClaimCommittedOwner {
		t.Fatalf("claim = %s, %v; want committed_owner", claim.Outcome, err)
	}
	if claim.Owner.ClaimID != "d1" {
		t.Fatalf("CommittedOwner resumed %q, want stored d1", claim.Owner.ClaimID)
	}
}

func TestP4B_EmptyHandoffCASMapSettlesInsteadOfInvalid(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	blockID := "blk-empty-cas"
	store.AddBlock(orgID, blockID, "hot", 0)
	owner := store.SeedBlockClaimForTest(orgID, blockID, "d1", time.Now().UTC())
	store.ForceEmptyHandoffCASOnceForTest()

	result, _ := store.CommitBlockDeleteOrphanHandoff(orgID, blockID, owner)
	if result.Outcome == BlockDeleteHandoffInvalid {
		t.Fatal("empty non-applied handoff CAS must SERIAL-settle, not return Invalid from the empty map")
	}

	store.SeedBlockHandoffForTest(orgID, blockID)
	store.ForceEmptyHandoffCASOnceForTest()
	result, err := store.CommitBlockDeleteOrphanHandoff(orgID, blockID, owner)
	if err != nil || result.Outcome != BlockDeleteHandoffAlreadyCommitted {
		t.Fatalf("empty CAS + settled committed row = %s, %v; want already_committed", result.Outcome, err)
	}
}

func TestP4B_CommittedOwnerRefsErrorLeavesQueueUntouched(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})
	orgID := uuid.New()
	blockID := testSHA256BlockID("p4b-refs-err")
	store.AddBlock(orgID, blockID, "hot", 0)
	candidateAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	candidate := ensureAndEnqueueBlockForTest(t, store, orgID, blockID, "hot", candidateAt, 0)
	original := store.QueueItems(orgID)[0]
	store.SeedBlockClaimForTest(orgID, blockID, "stored-d1", candidateAt)
	store.SeedBlockHandoffForTest(orgID, blockID)
	store.SetBlockHasReferencesGlobalErrForTest(errors.New("Cannot achieve consistency level EACH_QUORUM in DC dc-asia"))

	n, err := w.ProcessOnce(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("ProcessOnce() = (%d, %v), want committed-pending refusal", n, err)
	}
	block := store.GetBlock(orgID, blockID)
	if block == nil || block.GCClaimID != "stored-d1" || !orphanHandoffCommitted(block.GCOrphanHandoff) {
		t.Fatalf("refs-error released a committed authority: %+v", block)
	}
	if store.QueueCompleteCallsForTest() != 0 || store.QueueRequeueCallsForTest() != 0 || store.QueueFailCallsForTest() != 0 {
		t.Fatalf("queue lifecycle calls = complete:%d requeue:%d fail:%d, want all zero", store.QueueCompleteCallsForTest(), store.QueueRequeueCallsForTest(), store.QueueFailCallsForTest())
	}
	if len(store.QueueItems(orgID)) != 1 || store.QueueItems(orgID)[0].RetryCount != original.RetryCount {
		t.Fatalf("queue mutated after refs-error: %+v", store.QueueItems(orgID))
	}
	if _, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, candidate.Identity()); err != nil || !ok {
		t.Fatalf("candidate consumed after refs-error: ok=%v err=%v", ok, err)
	}
	if got := sp.DeletedBlocks(); len(got) != 0 {
		t.Fatalf("physical deletes = %v, want none", got)
	}
}

func TestP4B_TerminalLifecycleRefusesOrphanPublicationAndFinalize(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	blockID := "blk-terminal-d"
	d1 := testDeleteAuthority(blockID, "hot", MockCanonicalStorageKey(orgID.String(), blockID))
	committed := committedBlockDeleteAuthority(d1)
	firstSeen := time.Now().UTC().Truncate(time.Millisecond)
	created := store.StartBlockDeleteOrphan(orgID, blockID, committed, "sha1", firstSeen)
	if created.Outcome != StartBlockDeleteOrphanCreated {
		t.Fatalf("first publish = %s, want created: %v", created.Outcome, created.Cause)
	}
	if _, err := store.TerminateBlockDeleteLifecycle(orgID, blockID, committed); err != nil {
		t.Fatalf("terminate: %v", err)
	}

	replay := store.StartBlockDeleteOrphan(orgID, blockID, committed, "sha1-retry", time.Now().UTC())
	if replay.Outcome != StartBlockDeleteOrphanLifecycleAdvanced {
		t.Fatalf("terminal D StartBlockDeleteOrphan = %s, want lifecycle_advanced", replay.Outcome)
	}
	if store.S3OrphanCount() != 1 {
		t.Fatalf("terminal replay must not INSERT a second orphan; count=%d", store.S3OrphanCount())
	}

	finalized, err := store.FinalizeBlockDelete(orgID, blockID, committed)
	if finalized.ok() || finalized.Outcome == BlockDeleteAlreadyFinalized {
		t.Fatalf("terminal lifecycle finalize = %+v, %v; must not authorize S3", finalized, err)
	}
	if finalized.Outcome != BlockDeleteAlreadyComplete {
		t.Fatalf("outcome = %s, want already_complete", finalized.Outcome)
	}
}

func TestP4B_AlreadyFinalizedDoesNotAuthorizeWhenLifecycleTerminal(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	blockID := "blk-finalized-terminal"
	d1 := testDeleteAuthority(blockID, "hot", MockCanonicalStorageKey(orgID.String(), blockID))
	committed := committedBlockDeleteAuthority(d1)
	firstSeen := time.Now().UTC().Truncate(time.Millisecond)
	if result := store.StartBlockDeleteOrphan(orgID, blockID, committed, "sha1", firstSeen); result.Outcome != StartBlockDeleteOrphanCreated {
		t.Fatalf("publish = %s, want created: %v", result.Outcome, result.Cause)
	}
	if _, err := store.TerminateBlockDeleteLifecycle(orgID, blockID, committed); err != nil {
		t.Fatalf("terminate: %v", err)
	}

	finalized, err := store.FinalizeBlockDelete(orgID, blockID, committed)
	if finalized.Outcome != BlockDeleteAlreadyComplete || finalized.ok() {
		t.Fatalf("terminal lifecycle + absent blocks = %+v, %v; want already_complete (must not authorize S3 via AlreadyFinalized)", finalized, err)
	}
}

func TestP4B_ReplayAfterTerminalDoesNotRecreateOrphanOrDeleteP1(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})
	orgID := uuid.New()
	blockID := testSHA256BlockID("p4b-replay-d1")
	store.AddBlock(orgID, blockID, "hot", 0)
	candidateAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	ensureAndEnqueueBlockForTest(t, store, orgID, blockID, "hot", candidateAt, 0)
	d1 := store.SeedBlockClaimForTest(orgID, blockID, "stored-d1", candidateAt)
	store.SeedBlockHandoffForTest(orgID, blockID)

	n, err := w.ProcessOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("executor A ProcessOnce() = (%d, %v), want completion", n, err)
	}
	if store.BlockDeleteLifecyclePhaseForTest(orgID, blockID, d1.ClaimID) != BlockDeleteLifecyclePhaseTerminal {
		t.Fatal("executor A must leave the D tombstone terminal")
	}
	if store.S3OrphanCount() != 0 {
		t.Fatalf("executor A left orphan state: %+v", store.AllS3Orphans())
	}
	p1Deletes := append([]string(nil), sp.DeletedBlocks()...)

	store.AddBlock(orgID, blockID, "hot", 0)
	p2Key := MockCanonicalStorageKey(orgID.String(), blockID) + ".p2"
	store.SetBlockStorageKeyForTest(orgID, blockID, p2Key)

	stale := committedBlockDeleteAuthority(d1)
	replay := store.StartBlockDeleteOrphan(orgID, blockID, stale, "sha1-stale", time.Now().UTC())
	if replay.Outcome == StartBlockDeleteOrphanCreated || replay.Outcome == StartBlockDeleteOrphanSameAuthority {
		t.Fatalf("stale D1 after terminal = %s, must not republish", replay.Outcome)
	}
	if replay.Outcome != StartBlockDeleteOrphanLifecycleAdvanced {
		t.Fatalf("stale D1 after terminal = %s, want lifecycle_advanced", replay.Outcome)
	}
	if store.S3OrphanCount() != 0 {
		t.Fatalf("stale D1 recreated orphan state: %+v", store.AllS3Orphans())
	}

	finalized, _ := store.FinalizeBlockDelete(orgID, blockID, stale)
	if finalized.ok() || finalized.Outcome == BlockDeleteAlreadyFinalized {
		t.Fatalf("stale finalize after terminal = %+v; must not authorize S3", finalized)
	}
	if blk := store.GetBlock(orgID, blockID); blk == nil || blk.StorageKey != p2Key {
		t.Fatalf("stale D1 must not disturb P2: %+v", blk)
	}
	if got := sp.DeletedBlocks(); len(got) != len(p1Deletes) {
		t.Fatalf("stale executor emitted extra S3 deletes: %v (after A: %v)", got, p1Deletes)
	}
}

func TestP4B_StaleReplayDoesNotDeleteP1WhileWriterPutIsPreInstall(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})
	orgID := uuid.New()
	blockID := testSHA256BlockID("p4b-replay-preinstall")
	store.AddBlock(orgID, blockID, "hot", 0)
	candidateAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	ensureAndEnqueueBlockForTest(t, store, orgID, blockID, "hot", candidateAt, 0)
	d1 := store.SeedBlockClaimForTest(orgID, blockID, "stored-d1", candidateAt)
	store.SeedBlockHandoffForTest(orgID, blockID)
	p1Key := d1.Target.StorageKey

	n, err := w.ProcessOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("executor A ProcessOnce() = (%d, %v), want completion", n, err)
	}
	if store.GetBlock(orgID, blockID) != nil {
		t.Fatal("executor A must have dropped the canonical row")
	}
	afterA := append([]ScopedBlockDelete(nil), sp.ScopedBlockDeletes()...)
	if len(afterA) == 0 {
		t.Fatal("executor A must have issued the first exact-P1 delete")
	}

	// Writer re-PUTs P1 bytes but has not installed metadata yet. There is no
	// canonical row and no live orphan; a second DELETE of p1Key would remove
	// those in-flight bytes.
	stale := committedBlockDeleteAuthority(d1)
	replay := store.StartBlockDeleteOrphan(orgID, blockID, stale, "sha1-stale", time.Now().UTC())
	if replay.Outcome == StartBlockDeleteOrphanCreated || replay.Outcome == StartBlockDeleteOrphanSameAuthority {
		t.Fatalf("stale D1 during pre-install PUT = %s, must not republish", replay.Outcome)
	}
	if replay.Outcome != StartBlockDeleteOrphanLifecycleAdvanced {
		t.Fatalf("stale D1 during pre-install PUT = %s, want lifecycle_advanced", replay.Outcome)
	}
	finalized, _ := store.FinalizeBlockDelete(orgID, blockID, stale)
	if finalized.ok() || finalized.Outcome == BlockDeleteAlreadyFinalized {
		t.Fatalf("stale finalize during pre-install PUT = %+v; must not authorize S3", finalized)
	}

	got := sp.ScopedBlockDeletes()
	if len(got) != len(afterA) {
		t.Fatalf("stale executor emitted extra S3 deletes while a writer PUT of P1 is pre-install: %v (after A: %v)", got, afterA)
	}
	for _, del := range got[len(afterA):] {
		if del.StorageKey == p1Key {
			t.Fatalf("stale executor issued a second DELETE of P1 (%q) during a pre-install writer PUT", p1Key)
		}
	}
}

func TestP4B_WorkerAlreadyFinalizedWithTerminalLifecycleDoesNotDeleteS3(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})
	orgID := uuid.New()
	blockID := testSHA256BlockID("p4b-already-complete")
	store.AddBlock(orgID, blockID, "hot", 0)
	candidateAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	ensureAndEnqueueBlockForTest(t, store, orgID, blockID, "hot", candidateAt, 0)
	d1 := store.SeedBlockClaimForTest(orgID, blockID, "stored-d1", candidateAt)
	store.SeedBlockHandoffForTest(orgID, blockID)
	store.SeedBlockDeleteLifecycleForTest(orgID, blockID, d1, BlockDeleteLifecyclePhaseTerminal)

	n, err := w.ProcessOnce(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("ProcessOnce() = (%d, %v), want committed-pending / lifecycle-advanced refusal (must not authorize physical delete)", n, err)
	}
	if got := sp.DeletedBlocks(); len(got) != 0 {
		t.Fatalf("terminal lifecycle must not authorize physical delete: %v", got)
	}
	if store.GetBlock(orgID, blockID) == nil {
		t.Fatal("terminal D must not finalize the canonical row")
	}
}

func TestP4B_CommittedP1DoesNotPublishOrFinalizeP2(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	blockID := "blk-p1-p2"
	store.AddBlock(orgID, blockID, "hot", 0)
	p1 := store.SeedBlockClaimForTest(orgID, blockID, "d1", time.Now().UTC())
	store.SeedBlockHandoffForTest(orgID, blockID)
	p2Key := p1.Target.StorageKey + ".p2"
	p2 := BlockDeleteAuthority{
		Target:    BlockDeleteTarget{StorageClass: p1.Target.StorageClass, StorageKey: p2Key},
		ClaimID:   "d-p2",
		ClaimedAt: time.Now().UTC(),
	}

	handoff, err := store.CommitBlockDeleteOrphanHandoff(orgID, blockID, p2)
	if handoff.Outcome == BlockDeleteHandoffCommitted || handoff.Outcome == BlockDeleteHandoffAlreadyCommitted {
		t.Fatalf("P2 handoff against committed P1 = %s, %v; must not commit", handoff.Outcome, err)
	}
	finalized, _ := store.FinalizeBlockDelete(orgID, blockID, committedBlockDeleteAuthority(p2))
	if finalized.ok() {
		t.Fatalf("finalize P2 against committed P1 = %+v; must not apply", finalized)
	}
	if blk := store.GetBlock(orgID, blockID); blk == nil || blk.GCClaimID != "d1" {
		t.Fatalf("P2 lifecycle disturbed P1: %+v", blk)
	}
}

func TestP4B_CommittedBlockDeleteAuthorityIsOpaqueOutsidePackage(t *testing.T) {
	src, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	if !strings.Contains(text, "authority BlockDeleteAuthority") {
		t.Fatal("CommittedBlockDeleteAuthority must wrap an unexported field")
	}
	if strings.Contains(text, "type CommittedBlockDeleteAuthority struct {\n\tBlockDeleteAuthority") {
		t.Fatal("CommittedBlockDeleteAuthority must not embed an exported BlockDeleteAuthority")
	}
	if !strings.Contains(text, "func CommittedBlockDeleteAuthorityForTest") {
		t.Fatal("fixtures outside this package need CommittedBlockDeleteAuthorityForTest")
	}
}
