package gc

import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestP4B_StartBlockDeleteOrphanSourceContract(t *testing.T) {
	file := parseGCStoreFile(t)
	text := formattedGCFunction(t, file, "StartBlockDeleteOrphan")

	if !strings.Contains(text, "INSERT INTO gc_s3_orphans") {
		t.Fatal("StartBlockDeleteOrphan must publish the canonical orphan row")
	}
	if !strings.Contains(text, "IF NOT EXISTS") {
		t.Fatal("StartBlockDeleteOrphan must use a write-once IF NOT EXISTS LWT")
	}
	if !strings.Contains(text, "gc_claim_id") || !strings.Contains(text, "gc_claimed_at") {
		t.Fatal("StartBlockDeleteOrphan must persist the committed claim authority on the orphan row")
	}
	if strings.Contains(text, "UPDATE gc_s3_orphans") {
		t.Fatal("StartBlockDeleteOrphan must not overwrite an existing orphan lifecycle")
	}
	if !strings.Contains(text, "Consistency(gocql.EachQuorum)") {
		t.Fatal("StartBlockDeleteOrphan must pin regular consistency to EachQuorum")
	}
	if !strings.Contains(text, "SerialConsistency(gocql.Serial)") {
		t.Fatal("StartBlockDeleteOrphan must pin the LWT serial domain")
	}
	if !strings.Contains(text, "Idempotent(false)") || !strings.Contains(text, "NumRetries: 0") || !strings.Contains(text, "NonSpeculativeExecution") {
		t.Fatal("StartBlockDeleteOrphan must not hide an uncertain LWT behind driver retries")
	}
	if strings.Contains(text, "StartBlockDeleteOrphanNotPublished") {
		t.Fatal("StartBlockDeleteOrphan must not emit NotPublished; only SERIAL settlement may")
	}
	if got := strings.Count(text, "settleStartBlockDeleteOrphan"); got != 2 {
		t.Fatalf("StartBlockDeleteOrphan settlement helper calls = %d, want 2 (LWT error and empty non-applied CAS)", got)
	}
	if got := strings.Count(text, "confirmSameAuthorityOrphanResult"); got != 1 {
		t.Fatalf("StartBlockDeleteOrphan same-authority confirmation calls = %d, want 1 for the non-applied CAS path", got)
	}
	if got := strings.Count(text, "ensureS3OrphanProjectionResult"); got != 1 {
		t.Fatalf("StartBlockDeleteOrphan projection wrapper calls = %d, want 1 Created path; SameAuthority must confirm canonical visibility first", got)
	}

	settlement := findGCFunction(file, "settleS3OrphanState")
	if settlement == nil {
		t.Fatal("settleS3OrphanState not found")
	}
	if !gcQueryMethodHas(settlement, "SELECT storage_class, storage_key, first_seen_at", "Consistency", "Serial") {
		t.Fatal("settleS3OrphanState must read the canonical row at Consistency(gocql.Serial)")
	}

	settleHelper := formattedGCFunction(t, file, "settleStartBlockDeleteOrphan")
	if !strings.Contains(settleHelper, "confirmSameAuthorityOrphanResult") {
		t.Fatal("settled SameAuthority must confirm canonical EACH_QUORUM visibility before projection")
	}
	if strings.Contains(settleHelper, "ensureS3OrphanProjectionResult") {
		t.Fatal("settlement must not skip canonical visibility confirmation by publishing the projection directly")
	}

	confirm := formattedGCFunction(t, file, "confirmSameAuthorityOrphanResult")
	if !strings.Contains(confirm, "GetS3OrphanGlobal") {
		t.Fatal("SameAuthority confirmation must read the canonical row at EACH_QUORUM through GetS3OrphanGlobal")
	}
	if !strings.Contains(confirm, "classifyCanonicalOrphanVisibility") {
		t.Fatal("SameAuthority confirmation must classify the EACH_QUORUM row before authorizing finalize")
	}
	if !strings.Contains(confirm, "ensureS3OrphanProjectionResult") {
		t.Fatal("SameAuthority confirmation must repair the discovery projection only after canonical visibility")
	}
	if !strings.Contains(confirm, "confirmed.Outcome != StartBlockDeleteOrphanSameAuthority") {
		t.Fatal("LifecycleAdvanced and other non-SameAuthority outcomes must return before projection repair")
	}
	if findGCFunction(file, "ClassifySettledS3OrphanForTest") != nil {
		t.Fatal("SERIAL-only classification must not be an exported CassandraStore method")
	}

	global := formattedGCFunction(t, file, "GetS3OrphanGlobal")
	if !gcQueryMethodHas(findGCFunction(file, "GetS3OrphanGlobal"), "FROM gc_s3_orphans", "Consistency", "EachQuorum") {
		t.Fatal("GetS3OrphanGlobal must pin canonical visibility reads to EachQuorum")
	}
	if strings.Contains(global, "RecoveryPhase = strings.TrimSpace") {
		t.Fatal("GetS3OrphanGlobal must not trim recovery_phase before SameAuthority classification")
	}

	classifier := formattedGCFunction(t, file, "classifyNonAppliedOrphanCAS")
	if !strings.Contains(classifier, "NeedsSettlement: true") {
		t.Fatal("empty non-applied CAS must require SERIAL settlement")
	}
	if strings.Contains(classifier, "StartBlockDeleteOrphanNotPublished") {
		t.Fatal("classifyNonAppliedOrphanCAS must not emit NotPublished; empty CAS is not proof of absence")
	}

	visibility := formattedGCFunction(t, file, "classifyCanonicalOrphanVisibility")
	if strings.Contains(visibility, "TrimSpace(info.RecoveryPhase)") {
		t.Fatal("SameAuthority must compare recovery_phase exactly; trimming would accept a padded pending_s3 as authorization")
	}
	if !strings.Contains(visibility, "info.RecoveryPhase != S3OrphanPhasePendingS3") {
		t.Fatal("SameAuthority confirmation must require the exact pending_s3 token before authorizing finalize")
	}
	if !strings.Contains(visibility, "StartBlockDeleteOrphanLifecycleAdvanced") {
		t.Fatal("a visible same-P row past pending_s3 must be classified as lifecycle_advanced, not SameAuthority")
	}
}

func TestP4B_OrphanProjectionPinsEachQuorum(t *testing.T) {
	file := parseGCStoreFile(t)
	projection := findGCFunction(file, "upsertS3OrphanProjection")
	if projection == nil {
		t.Fatal("upsertS3OrphanProjection not found")
	}
	if !gcQueryMethodHas(projection, "INSERT INTO gc_s3_orphans_by_day", "Consistency", "EachQuorum") {
		t.Fatal("upsertS3OrphanProjection must pin discovery publication to gocql.EachQuorum")
	}
}

func TestP4B_CanonicalOrphanSchemaDoesNotDisableBlockingReadRepair(t *testing.T) {
	source, err := os.ReadFile("../db/migrations/001_initial_schema.cql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	start := strings.Index(text, "CREATE TABLE IF NOT EXISTS gc_s3_orphans (")
	if start < 0 {
		t.Fatal("gc_s3_orphans table definition not found")
	}
	rest := text[start:]
	end := strings.Index(rest, "CREATE TABLE IF NOT EXISTS gc_s3_orphans_by_day")
	if end < 0 {
		t.Fatal("could not bound the gc_s3_orphans table definition")
	}
	table := strings.ToLower(rest[:end])
	if strings.Contains(table, "read_repair") && strings.Contains(table, "none") {
		t.Fatal("gc_s3_orphans must not set read_repair='NONE'; SameAuthority confirmation relies on blocking read repair at EACH_QUORUM")
	}

	entries, err := os.ReadDir(filepath.Join("..", "db", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".cql") {
			continue
		}
		body, err := os.ReadFile(filepath.Join("..", "db", "migrations", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		lower := strings.ToLower(string(body))
		if !strings.Contains(lower, "read_repair") || !strings.Contains(lower, "none") {
			continue
		}
		if strings.Contains(lower, "alter table gc_s3_orphans ") || strings.Contains(lower, "alter table gc_s3_orphans\n") || strings.Contains(lower, "alter table gc_s3_orphans\t") {
			t.Fatalf("%s disables blocking read repair on gc_s3_orphans; SameAuthority confirmation relies on BLOCKING", entry.Name())
		}
	}
}

func TestP4B_EmptyNonAppliedCASRequiresSerialSettlement(t *testing.T) {
	proposed := testDeleteAuthority("p4b-empty-cas", "hot", MockCanonicalStorageKey(uuid.NewString(), "p4b-empty-cas"))
	got := classifyNonAppliedOrphanCAS(map[string]interface{}{}, proposed)
	if !got.NeedsSettlement {
		t.Error("empty non-applied CAS must be settled in the SERIAL domain")
	}
	if got.Result.Outcome == StartBlockDeleteOrphanNotPublished {
		t.Error("empty non-applied CAS is not proof of absence")
	}
	if t.Failed() {
		return
	}

	same := classifyNonAppliedOrphanCAS(map[string]interface{}{
		"storage_class": proposed.Target.StorageClass,
		"storage_key":   proposed.Target.StorageKey,
		"first_seen_at": time.Unix(1, 0).UTC(),
		"gc_claim_id":   proposed.ClaimID,
		"gc_claimed_at": proposed.ClaimedAt,
	}, proposed)
	if same.NeedsSettlement || same.Result.Outcome != StartBlockDeleteOrphanSameAuthority {
		t.Fatalf("usable same-authority CAS = settlement:%v outcome:%s, want same_authority without settlement", same.NeedsSettlement, same.Result.Outcome)
	}

	unbound := classifyNonAppliedOrphanCAS(map[string]interface{}{
		"storage_class": proposed.Target.StorageClass,
		"storage_key":   proposed.Target.StorageKey,
		"first_seen_at": time.Unix(1, 0).UTC(),
	}, proposed)
	if unbound.NeedsSettlement || unbound.Result.Outcome != StartBlockDeleteOrphanUnboundAuthority {
		t.Fatalf("legacy unbound CAS = settlement:%v outcome:%s, want unbound_authority", unbound.NeedsSettlement, unbound.Result.Outcome)
	}

	differentD := classifyNonAppliedOrphanCAS(map[string]interface{}{
		"storage_class": proposed.Target.StorageClass,
		"storage_key":   proposed.Target.StorageKey,
		"first_seen_at": time.Unix(1, 0).UTC(),
		"gc_claim_id":   "other-claim",
		"gc_claimed_at": proposed.ClaimedAt,
	}, proposed)
	if differentD.NeedsSettlement || differentD.Result.Outcome != StartBlockDeleteOrphanDifferentAuthority {
		t.Fatalf("same-P different-D CAS = settlement:%v outcome:%s, want different_authority", differentD.NeedsSettlement, differentD.Result.Outcome)
	}

	different := classifyNonAppliedOrphanCAS(map[string]interface{}{
		"storage_class": "cold",
		"storage_key":   proposed.Target.StorageKey,
		"first_seen_at": time.Unix(1, 0).UTC(),
		"gc_claim_id":   proposed.ClaimID,
		"gc_claimed_at": proposed.ClaimedAt,
	}, proposed)
	if different.NeedsSettlement || different.Result.Outcome != StartBlockDeleteOrphanDifferentTarget {
		t.Fatalf("usable different-target CAS = settlement:%v outcome:%s, want different_target", different.NeedsSettlement, different.Result.Outcome)
	}
}

func TestP4B_SettledOrphanClassification(t *testing.T) {
	proposed := testDeleteAuthority("p4b-settled", "hot", "blocks/org/aa/bb/key")
	row := s3OrphanCASRow{Target: proposed.Target, Authority: proposed, FirstSeenAt: time.Unix(2, 0).UTC()}

	absent := resultFromSettledS3Orphan(s3OrphanCASRow{}, false, nil, proposed, errors.New("lwt timeout"))
	if absent.Outcome != StartBlockDeleteOrphanNotPublished {
		t.Fatalf("SERIAL absence = %s, want not_published", absent.Outcome)
	}

	same := resultFromSettledS3Orphan(row, true, nil, proposed, errors.New("lwt timeout"))
	if same.Outcome != StartBlockDeleteOrphanSameAuthority || !same.FirstSeenAt.Equal(row.FirstSeenAt) {
		t.Fatalf("SERIAL same-authority = %+v, want same_authority at stored token", same)
	}

	different := resultFromSettledS3Orphan(s3OrphanCASRow{
		Target:      BlockDeleteTarget{StorageClass: "cold", StorageKey: proposed.Target.StorageKey},
		Authority:   testDeleteAuthority("p4b-settled", "cold", proposed.Target.StorageKey),
		FirstSeenAt: row.FirstSeenAt,
	}, true, nil, proposed, nil)
	if different.Outcome != StartBlockDeleteOrphanDifferentTarget {
		t.Fatalf("SERIAL different-target = %s, want different_target", different.Outcome)
	}

	differentD := resultFromSettledS3Orphan(s3OrphanCASRow{
		Target: proposed.Target,
		Authority: BlockDeleteAuthority{
			Target:    proposed.Target,
			ClaimID:   "other-claim",
			ClaimedAt: proposed.ClaimedAt,
		},
		FirstSeenAt: row.FirstSeenAt,
	}, true, nil, proposed, nil)
	if differentD.Outcome != StartBlockDeleteOrphanDifferentAuthority {
		t.Fatalf("SERIAL same-P different-D = %s, want different_authority", differentD.Outcome)
	}

	unbound := resultFromSettledS3Orphan(s3OrphanCASRow{
		Target:      proposed.Target,
		FirstSeenAt: row.FirstSeenAt,
	}, true, nil, proposed, nil)
	if unbound.Outcome != StartBlockDeleteOrphanUnboundAuthority {
		t.Fatalf("SERIAL unbound = %s, want unbound_authority", unbound.Outcome)
	}

	unreadable := resultFromSettledS3Orphan(s3OrphanCASRow{}, false, errors.New("serial unavailable"), proposed, errors.New("lwt timeout"))
	if unreadable.Outcome != StartBlockDeleteOrphanAmbiguous {
		t.Fatalf("unreadable settlement = %s, want ambiguous", unreadable.Outcome)
	}

	malformed := resultFromSettledS3Orphan(s3OrphanCASRow{}, true, errors.New("incomplete identity"), proposed, nil)
	if malformed.Outcome != StartBlockDeleteOrphanInvalid {
		t.Fatalf("malformed settlement = %s, want invalid", malformed.Outcome)
	}
}

func TestP4B_CanonicalVisibilityClassification(t *testing.T) {
	proposed := testDeleteAuthority("p4b-visible", "hot", "blocks/org/aa/bb/key")
	token := time.Unix(3, 0).UTC()
	prior := StartBlockDeleteOrphanResult{Outcome: StartBlockDeleteOrphanSameAuthority, FirstSeenAt: token, ExistingTarget: proposed.Target, ExistingAuthority: proposed, Submitted: true}

	missing := classifyCanonicalOrphanVisibility(S3OrphanInfo{}, false, nil, proposed, token, prior)
	if missing.Outcome != StartBlockDeleteOrphanAmbiguous {
		t.Fatalf("EACH_QUORUM miss after SERIAL hit = %s, want ambiguous not not_published", missing.Outcome)
	}
	if missing.Outcome == StartBlockDeleteOrphanNotPublished {
		t.Fatal("EACH_QUORUM miss must not release the claim as NotPublished")
	}

	readErr := classifyCanonicalOrphanVisibility(S3OrphanInfo{}, false, errors.New("dc-na unavailable"), proposed, token, prior)
	if readErr.Outcome != StartBlockDeleteOrphanAmbiguous {
		t.Fatalf("EACH_QUORUM error = %s, want ambiguous", readErr.Outcome)
	}

	matching := S3OrphanInfo{
		StorageClass:  proposed.Target.StorageClass,
		StorageKey:    proposed.Target.StorageKey,
		FirstSeenAt:   token,
		RecoveryPhase: S3OrphanPhasePendingS3,
		Authority:     proposed,
	}
	visible := classifyCanonicalOrphanVisibility(matching, true, nil, proposed, token, prior)
	if visible.Outcome != StartBlockDeleteOrphanSameAuthority {
		t.Fatalf("matching EACH_QUORUM row = %s, want same_authority", visible.Outcome)
	}

	other := matching
	other.StorageClass = "cold"
	other.Authority = testDeleteAuthority("p4b-visible", "cold", proposed.Target.StorageKey)
	gotOther := classifyCanonicalOrphanVisibility(other, true, nil, proposed, token, prior)
	if gotOther.Outcome != StartBlockDeleteOrphanDifferentTarget {
		t.Fatalf("visible different target = %s, want different_target", gotOther.Outcome)
	}

	differentD := matching
	differentD.Authority = BlockDeleteAuthority{Target: proposed.Target, ClaimID: "other-claim", ClaimedAt: proposed.ClaimedAt}
	gotDifferentD := classifyCanonicalOrphanVisibility(differentD, true, nil, proposed, token, prior)
	if gotDifferentD.Outcome != StartBlockDeleteOrphanDifferentAuthority {
		t.Fatalf("visible same-P different-D = %s, want different_authority", gotDifferentD.Outcome)
	}

	unbound := matching
	unbound.Authority = BlockDeleteAuthority{}
	gotUnbound := classifyCanonicalOrphanVisibility(unbound, true, nil, proposed, token, prior)
	if gotUnbound.Outcome != StartBlockDeleteOrphanUnboundAuthority {
		t.Fatalf("visible unbound = %s, want unbound_authority", gotUnbound.Outcome)
	}

	mismatchedToken := matching
	mismatchedToken.FirstSeenAt = token.Add(time.Second)
	gotMismatch := classifyCanonicalOrphanVisibility(mismatchedToken, true, nil, proposed, token, prior)
	if gotMismatch.Outcome != StartBlockDeleteOrphanInvalid {
		t.Fatalf("EACH_QUORUM first_seen_at mismatch = %s, want invalid", gotMismatch.Outcome)
	}

	incomplete := matching
	incomplete.StorageKey = ""
	gotIncomplete := classifyCanonicalOrphanVisibility(incomplete, true, nil, proposed, token, prior)
	if gotIncomplete.Outcome != StartBlockDeleteOrphanInvalid {
		t.Fatalf("EACH_QUORUM incomplete identity = %s, want invalid", gotIncomplete.Outcome)
	}

	advanced := matching
	advanced.RecoveryPhase = S3OrphanPhasePendingMappingCleanup
	gotAdvanced := classifyCanonicalOrphanVisibility(advanced, true, nil, proposed, token, prior)
	if gotAdvanced.Outcome != StartBlockDeleteOrphanLifecycleAdvanced {
		t.Fatalf("pending_mapping_cleanup must not authorize finalize: got %s", gotAdvanced.Outcome)
	}

	emptyPhase := matching
	emptyPhase.RecoveryPhase = ""
	gotEmpty := classifyCanonicalOrphanVisibility(emptyPhase, true, nil, proposed, token, prior)
	if gotEmpty.Outcome != StartBlockDeleteOrphanLifecycleAdvanced {
		t.Fatalf("empty recovery_phase must not authorize finalize: got %s", gotEmpty.Outcome)
	}

	paddedPhase := matching
	paddedPhase.RecoveryPhase = " " + S3OrphanPhasePendingS3 + " "
	gotPadded := classifyCanonicalOrphanVisibility(paddedPhase, true, nil, proposed, token, prior)
	if gotPadded.Outcome != StartBlockDeleteOrphanLifecycleAdvanced {
		t.Fatalf("padded recovery_phase must not authorize finalize: got %s", gotPadded.Outcome)
	}
}

func parseGCStoreFile(t *testing.T) *ast.File {
	t.Helper()
	source, err := os.ReadFile("store_cassandra.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "store_cassandra.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func formattedGCFunction(t *testing.T, file *ast.File, name string) string {
	t.Helper()
	function := findGCFunction(file, name)
	if function == nil {
		t.Fatalf("%s not found", name)
	}
	var formatted bytes.Buffer
	if err := format.Node(&formatted, token.NewFileSet(), function); err != nil {
		t.Fatalf("format %s: %v", name, err)
	}
	return formatted.String()
}

func TestP4B_WorkerDifferentTargetLeavesCommittedClaimUntouched(t *testing.T) {
	store := NewMockStore()
	w := NewWorker(store, &MockStorageProvider{}, NewQueue(store), 100, 0, false, &Stats{})
	orgID := uuid.New()
	blockID := testSHA256BlockID("p4b-different-target")
	store.AddBlock(orgID, blockID, "hot", 0)
	candidateAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	candidate := ensureAndEnqueueBlockForTest(t, store, orgID, blockID, "hot", candidateAt, 0)
	seedS3Orphan(t, store, orgID, blockID, "cold", "sha1-existing", "existing failure", candidateAt)

	if n, err := w.ProcessOnce(context.Background()); err != nil || n != 0 {
		t.Fatalf("ProcessOnce() = (%d, %v), want handled refusal without successful completion", n, err)
	}

	block := store.GetBlock(orgID, blockID)
	if block == nil {
		t.Fatal("canonical block must survive a different orphan target")
	}
	if block.GCState != "deleting" || block.GCClaimID == "" || !orphanHandoffCommitted(block.GCOrphanHandoff) {
		t.Fatalf("different target after handoff must retain the committed claim, got state=%q claim=%q handoff=%v", block.GCState, block.GCClaimID, block.GCOrphanHandoff)
	}
	items := store.QueueItems(orgID)
	if len(items) != 1 || items[0].RetryCount != 0 {
		t.Fatalf("queue after different target = %+v, want the same item with retry_count=0", items)
	}
	if store.QueueRequeueCallsForTest() != 0 || store.QueueCompleteCallsForTest() != 0 || store.QueueFailCallsForTest() != 0 {
		t.Fatalf("queue lifecycle calls = complete:%d requeue:%d fail:%d, want all zero after committed handoff", store.QueueCompleteCallsForTest(), store.QueueRequeueCallsForTest(), store.QueueFailCallsForTest())
	}
	if _, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, candidate.Identity()); err != nil || !ok {
		t.Fatalf("candidate was consumed after a confirmed orphan conflict: ok=%v err=%v", ok, err)
	}
	orphans := store.AllS3Orphans()
	if len(orphans) != 1 || orphans[0].StorageClass != "cold" || orphans[0].ExternalSHA1 != "sha1-existing" {
		t.Fatalf("existing orphan changed after conflict: %+v", orphans)
	}
}

func TestP4B_WorkerProjectionUnconfirmedLeavesClaimAndQueueUntouched(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})
	orgID := uuid.New()
	blockID := testSHA256BlockID("p4b-projection-unconfirmed")
	store.AddBlock(orgID, blockID, "hot", 0)
	candidateAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	candidate := ensureAndEnqueueBlockForTest(t, store, orgID, blockID, "hot", candidateAt, 0)
	store.SetStartBlockDeleteOrphanProjectionErrOnceForTest(context.DeadlineExceeded)

	if n, err := w.ProcessOnce(context.Background()); err != nil || n != 0 {
		t.Fatalf("ProcessOnce() = (%d, %v), want untouched publication refusal", n, err)
	}

	block := store.GetBlock(orgID, blockID)
	if block == nil || block.GCState != "deleting" || block.GCClaimID == "" {
		t.Fatalf("projection uncertainty must retain the claim, got block=%+v", block)
	}
	items := store.QueueItems(orgID)
	if len(items) != 1 || items[0].RetryCount != 0 {
		t.Fatalf("queue after projection uncertainty = %+v, want untouched item", items)
	}
	if store.QueueRequeueCallsForTest() != 0 || store.QueueCompleteCallsForTest() != 0 || store.QueueFailCallsForTest() != 0 {
		t.Fatalf("queue lifecycle calls = complete:%d requeue:%d fail:%d, want all zero", store.QueueCompleteCallsForTest(), store.QueueRequeueCallsForTest(), store.QueueFailCallsForTest())
	}
	if _, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, candidate.Identity()); err != nil || !ok {
		t.Fatalf("candidate was consumed after projection uncertainty: ok=%v err=%v", ok, err)
	}
	if store.S3OrphanCount() != 1 || len(sp.DeletedBlocks()) != 0 {
		t.Fatalf("projection uncertainty must not delete S3 or clear orphan state: orphans=%d deletes=%v", store.S3OrphanCount(), sp.DeletedBlocks())
	}
}

func TestP4B_WorkerAmbiguousAndInvalidLeaveQueueUntouched(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*MockStore, uuid.UUID, string, time.Time)
	}{
		{
			name: "ambiguous publication",
			configure: func(store *MockStore, _ uuid.UUID, _ string, _ time.Time) {
				store.SetStartBlockDeleteOrphanAmbiguousOnceForTest()
			},
		},
		{
			name: "malformed existing row",
			configure: func(store *MockStore, orgID uuid.UUID, blockID string, candidateAt time.Time) {
				seedS3Orphan(t, store, orgID, blockID, "hot", "sha1-existing", "existing failure", candidateAt)
				store.SetS3OrphanStorageKeyForTest(orgID, blockID, " ")
			},
		},
		{
			name: "advanced recovery phase",
			configure: func(store *MockStore, orgID uuid.UUID, blockID string, candidateAt time.Time) {
				firstSeenAt := seedS3Orphan(t, store, orgID, blockID, "hot", "sha1-existing", "", candidateAt)
				if err := store.MarkS3OrphanMappingCleanupPending(orgID, blockID, "sha1-existing", firstSeenAt.Add(time.Minute)); err != nil {
					t.Fatalf("advance orphan phase: %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMockStore()
			w := NewWorker(store, &MockStorageProvider{}, NewQueue(store), 100, 0, false, &Stats{})
			orgID := uuid.New()
			blockID := testSHA256BlockID("p4b-unsettled-" + tc.name)
			store.AddBlock(orgID, blockID, "hot", 0)
			candidateAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
			candidate := ensureAndEnqueueBlockForTest(t, store, orgID, blockID, "hot", candidateAt, 0)
			originalItem := store.QueueItems(orgID)[0]
			tc.configure(store, orgID, blockID, candidateAt)

			if n, err := w.ProcessOnce(context.Background()); err != nil || n != 0 {
				t.Fatalf("ProcessOnce() = (%d, %v), want fail-closed refusal", n, err)
			}
			block := store.GetBlock(orgID, blockID)
			if block == nil || block.GCState != "deleting" || block.GCClaimID == "" {
				t.Fatalf("publication uncertainty must retain the claim: block=%+v", block)
			}
			items := store.QueueItems(orgID)
			if len(items) != 1 || items[0].RetryCount != originalItem.RetryCount || items[0].QueuedAt != originalItem.QueuedAt || items[0].IdentityAt != originalItem.IdentityAt || items[0].BlockGCCandidateIdentity != originalItem.BlockGCCandidateIdentity {
				t.Fatalf("queue after %s = %+v, want exact original item %+v", tc.name, items, originalItem)
			}
			if store.QueueCompleteCallsForTest() != 0 || store.QueueRequeueCallsForTest() != 0 || store.QueueFailCallsForTest() != 0 {
				t.Fatalf("queue lifecycle calls = complete:%d requeue:%d fail:%d, want all zero", store.QueueCompleteCallsForTest(), store.QueueRequeueCallsForTest(), store.QueueFailCallsForTest())
			}
			if _, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, candidate.Identity()); err != nil || !ok {
				t.Fatalf("candidate was consumed after %s: ok=%v err=%v", tc.name, ok, err)
			}
		})
	}
}

func TestP4B_WorkerOrphanRefusalAfterHandoffLeavesQueueUntouched(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*MockStore, uuid.UUID, string, time.Time)
	}{
		{
			name: "different target",
			configure: func(store *MockStore, orgID uuid.UUID, blockID string, candidateAt time.Time) {
				seedS3Orphan(t, store, orgID, blockID, "cold", "sha1-existing", "existing failure", candidateAt)
			},
		},
		{
			name: "not published",
			configure: func(store *MockStore, _ uuid.UUID, _ string, _ time.Time) {
				store.SetStartBlockDeleteOrphanNotPublishedOnceForTest()
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMockStore()
			w := NewWorker(store, &MockStorageProvider{}, NewQueue(store), 100, 0, false, &Stats{})
			orgID := uuid.New()
			blockID := testSHA256BlockID("p4b-not-owner-" + tc.name)
			store.AddBlock(orgID, blockID, "hot", 0)
			candidateAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
			candidate := ensureAndEnqueueBlockForTest(t, store, orgID, blockID, "hot", candidateAt, 0)
			originalItem := store.QueueItems(orgID)[0]
			tc.configure(store, orgID, blockID, candidateAt)

			if n, err := w.ProcessOnce(context.Background()); err != nil || n != 0 {
				t.Fatalf("ProcessOnce() = (%d, %v), want untouched committed-pending refusal", n, err)
			}

			block := store.GetBlock(orgID, blockID)
			if block == nil || block.GCState != "deleting" || block.GCClaimID == "" || !orphanHandoffCommitted(block.GCOrphanHandoff) {
				t.Fatalf("committed claim was not preserved: block=%+v", block)
			}
			items := store.QueueItems(orgID)
			if len(items) != 1 || items[0].RetryCount != originalItem.RetryCount || items[0].QueuedAt != originalItem.QueuedAt || items[0].IdentityAt != originalItem.IdentityAt || items[0].BlockGCCandidateIdentity != originalItem.BlockGCCandidateIdentity {
				t.Fatalf("queue after post-handoff refusal = %+v, want exact original item %+v", items, originalItem)
			}
			if store.QueueCompleteCallsForTest() != 0 || store.QueueRequeueCallsForTest() != 0 || store.QueueFailCallsForTest() != 0 {
				t.Fatalf("queue lifecycle calls = complete:%d requeue:%d fail:%d, want all zero", store.QueueCompleteCallsForTest(), store.QueueRequeueCallsForTest(), store.QueueFailCallsForTest())
			}
			if _, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, candidate.Identity()); err != nil || !ok {
				t.Fatalf("candidate was consumed after post-handoff refusal: ok=%v err=%v", ok, err)
			}
		})
	}
}

func TestP4B_WorkerCanonicalVisibilityUnconfirmedLeavesQueueUntouched(t *testing.T) {
	store := NewMockStore()
	w := NewWorker(store, &MockStorageProvider{}, NewQueue(store), 100, 0, false, &Stats{})
	orgID := uuid.New()
	blockID := testSHA256BlockID("p4b-canonical-unconfirmed")
	store.AddBlock(orgID, blockID, "hot", 0)
	candidateAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	candidate := ensureAndEnqueueBlockForTest(t, store, orgID, blockID, "hot", candidateAt, 0)
	seedS3Orphan(t, store, orgID, blockID, "hot", "sha1-existing", "", candidateAt)
	store.SetStartBlockDeleteOrphanCanonicalUnconfirmedOnceForTest()

	if n, err := w.ProcessOnce(context.Background()); err != nil || n != 0 {
		t.Fatalf("ProcessOnce() = (%d, %v), want fail-closed refusal", n, err)
	}
	block := store.GetBlock(orgID, blockID)
	if block == nil || block.GCState != "deleting" || block.GCClaimID == "" {
		t.Fatalf("canonical visibility uncertainty must retain the claim: block=%+v", block)
	}
	if store.QueueCompleteCallsForTest() != 0 || store.QueueRequeueCallsForTest() != 0 || store.QueueFailCallsForTest() != 0 {
		t.Fatalf("queue lifecycle calls = complete:%d requeue:%d fail:%d, want all zero", store.QueueCompleteCallsForTest(), store.QueueRequeueCallsForTest(), store.QueueFailCallsForTest())
	}
	if _, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, candidate.Identity()); err != nil || !ok {
		t.Fatalf("candidate was consumed after canonical visibility uncertainty: ok=%v err=%v", ok, err)
	}
}

func TestP4B_WorkerPublicationRefusalRecordsBlockPathNotOrphan(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*MockStore, uuid.UUID, string, time.Time)
	}{
		{
			name: "ambiguous publication",
			configure: func(store *MockStore, _ uuid.UUID, _ string, _ time.Time) {
				store.SetStartBlockDeleteOrphanAmbiguousOnceForTest()
			},
		},
		{
			name: "projection unconfirmed",
			configure: func(store *MockStore, _ uuid.UUID, _ string, _ time.Time) {
				store.SetStartBlockDeleteOrphanProjectionErrOnceForTest(context.DeadlineExceeded)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMockStore()
			w := NewWorker(store, &MockStorageProvider{}, NewQueue(store), 100, 0, false, &Stats{})
			w.clock = advancingClock(time.Now())
			resetDestructivePairForTest(destructivePathBlock, destructivePathOrphan)

			orgID := uuid.New()
			blockID := testSHA256BlockID("p4b-path-" + tc.name)
			store.AddBlock(orgID, blockID, "hot", 0)
			candidateAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
			ensureAndEnqueueBlockForTest(t, store, orgID, blockID, "hot", candidateAt, 0)
			tc.configure(store, orgID, blockID, candidateAt)

			if n, err := w.ProcessOnce(context.Background()); err != nil || n != 0 {
				t.Fatalf("ProcessOnce() = (%d, %v), want publication refusal", n, err)
			}

			blocked, livenessSuccess := destructivePairForTest(t, destructivePathBlock)
			if blocked <= livenessSuccess {
				t.Fatalf("block path last_blocked=%v last_liveness_success=%v, want blocked later: publication refusals belong to the worker path whose liveness this walk published", blocked, livenessSuccess)
			}
			orphanBlocked, orphanSuccess := destructivePairForTest(t, destructivePathOrphan)
			if orphanBlocked != 0 || orphanSuccess != 0 {
				t.Fatalf("orphan path moved to blocked=%v success=%v; a processBlock publication refusal must not speak for recovery", orphanBlocked, orphanSuccess)
			}
		})
	}
}

func TestP4B_WorkerLifecycleAdvancedDoesNotFinalize(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})
	orgID := uuid.New()
	blockID := testSHA256BlockID("p4b-lifecycle-advanced")
	store.AddBlock(orgID, blockID, "hot", 0)
	store.AddBlockMapping(orgID, "sha1-new", blockID)
	firstSeenAt := seedS3Orphan(t, store, orgID, blockID, "hot", "sha1-old", "prev", time.Now().Add(-time.Hour))
	if err := store.MarkS3OrphanMappingCleanupPending(orgID, blockID, "sha1-old", firstSeenAt.Add(5*time.Minute)); err != nil {
		t.Fatalf("advance stale orphan phase: %v", err)
	}
	candidateAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	candidate := ensureAndEnqueueBlockForTest(t, store, orgID, blockID, "hot", candidateAt, 0)

	n, err := w.ProcessOnce(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("ProcessOnce() = (%d, %v), want fail-closed refusal", n, err)
	}
	block := store.GetBlock(orgID, blockID)
	if block == nil || block.GCState != "deleting" || block.GCClaimID == "" {
		t.Fatalf("pending_mapping_cleanup must not authorize finalize: block=%+v", block)
	}
	if store.QueueCompleteCallsForTest() != 0 || store.QueueRequeueCallsForTest() != 0 || store.QueueFailCallsForTest() != 0 {
		t.Fatalf("queue lifecycle calls = complete:%d requeue:%d fail:%d, want all zero", store.QueueCompleteCallsForTest(), store.QueueRequeueCallsForTest(), store.QueueFailCallsForTest())
	}
	if _, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, candidate.Identity()); err != nil || !ok {
		t.Fatalf("candidate was consumed after lifecycle-advanced refusal: ok=%v err=%v", ok, err)
	}
	if got := sp.DeletedBlocks(); len(got) != 0 {
		t.Fatalf("lifecycle-advanced publication must not delete S3: %v", got)
	}

	recovered, recErr := w.RecoverS3Orphans(context.Background(), 100)
	if recErr != nil || recovered != 1 {
		t.Fatalf("RecoverS3Orphans() = (%d, %v), want 1 post-S3 finalization without a physical delete", recovered, recErr)
	}
	if store.GetBlock(orgID, blockID) == nil {
		t.Fatal("recovery of pending_mapping_cleanup must not remove the still-claimed block")
	}
	if got := sp.DeletedBlocks(); len(got) != 0 {
		t.Fatalf("recovery must skip the physical delete for pending_mapping_cleanup, got %v", got)
	}
}
