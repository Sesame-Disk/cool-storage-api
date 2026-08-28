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
	if got := strings.Count(text, "confirmSameTargetOrphanResult"); got != 1 {
		t.Fatalf("StartBlockDeleteOrphan same-target confirmation calls = %d, want 1 for the non-applied CAS path", got)
	}
	if got := strings.Count(text, "ensureS3OrphanProjectionResult"); got != 1 {
		t.Fatalf("StartBlockDeleteOrphan projection wrapper calls = %d, want 1 Created path; SameTarget must confirm canonical visibility first", got)
	}

	settlement := findGCFunction(file, "settleS3OrphanState")
	if settlement == nil {
		t.Fatal("settleS3OrphanState not found")
	}
	if !gcQueryMethodHas(settlement, "SELECT storage_class, storage_key, first_seen_at", "Consistency", "Serial") {
		t.Fatal("settleS3OrphanState must read the canonical row at Consistency(gocql.Serial)")
	}

	settleHelper := formattedGCFunction(t, file, "settleStartBlockDeleteOrphan")
	if !strings.Contains(settleHelper, "confirmSameTargetOrphanResult") {
		t.Fatal("settled SameTarget must confirm canonical EACH_QUORUM visibility before projection")
	}
	if strings.Contains(settleHelper, "ensureS3OrphanProjectionResult") {
		t.Fatal("settlement must not skip canonical visibility confirmation by publishing the projection directly")
	}

	confirm := formattedGCFunction(t, file, "confirmSameTargetOrphanResult")
	if !strings.Contains(confirm, "GetS3OrphanGlobal") {
		t.Fatal("SameTarget confirmation must read the canonical row at EACH_QUORUM through GetS3OrphanGlobal")
	}
	if !strings.Contains(confirm, "classifyCanonicalOrphanVisibility") {
		t.Fatal("SameTarget confirmation must classify the EACH_QUORUM row before authorizing finalize")
	}
	if !strings.Contains(confirm, "ensureS3OrphanProjectionResult") {
		t.Fatal("SameTarget confirmation must repair the discovery projection only after canonical visibility")
	}

	global := findGCFunction(file, "GetS3OrphanGlobal")
	if global == nil {
		t.Fatal("GetS3OrphanGlobal not found")
	}
	if !gcQueryMethodHas(global, "FROM gc_s3_orphans", "Consistency", "EachQuorum") {
		t.Fatal("GetS3OrphanGlobal must pin canonical visibility reads to EachQuorum")
	}

	classifier := formattedGCFunction(t, file, "classifyNonAppliedOrphanCAS")
	if !strings.Contains(classifier, "NeedsSettlement: true") {
		t.Fatal("empty non-applied CAS must require SERIAL settlement")
	}
	if strings.Contains(classifier, "StartBlockDeleteOrphanNotPublished") {
		t.Fatal("classifyNonAppliedOrphanCAS must not emit NotPublished; empty CAS is not proof of absence")
	}

	visibility := formattedGCFunction(t, file, "classifyCanonicalOrphanVisibility")
	if !strings.Contains(visibility, "S3OrphanPhasePendingS3") {
		t.Fatal("SameTarget confirmation must require pending_s3 before authorizing finalize")
	}
	if !strings.Contains(visibility, "StartBlockDeleteOrphanLifecycleAdvanced") {
		t.Fatal("a visible same-P row past pending_s3 must be classified as lifecycle_advanced, not SameTarget")
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
		t.Fatal("gc_s3_orphans must not set read_repair='NONE'; SameTarget confirmation relies on blocking read repair at EACH_QUORUM")
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
			t.Fatalf("%s disables blocking read repair on gc_s3_orphans; SameTarget confirmation relies on BLOCKING", entry.Name())
		}
	}
}

func TestP4B_EmptyNonAppliedCASRequiresSerialSettlement(t *testing.T) {
	proposed := BlockDeleteTarget{StorageClass: "hot", StorageKey: MockCanonicalStorageKey(uuid.NewString(), "p4b-empty-cas")}
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
		"storage_class": "hot",
		"storage_key":   proposed.StorageKey,
		"first_seen_at": time.Unix(1, 0).UTC(),
	}, proposed)
	if same.NeedsSettlement || same.Result.Outcome != StartBlockDeleteOrphanSameTarget {
		t.Fatalf("usable same-target CAS = settlement:%v outcome:%s, want same_target without settlement", same.NeedsSettlement, same.Result.Outcome)
	}

	different := classifyNonAppliedOrphanCAS(map[string]interface{}{
		"storage_class": "cold",
		"storage_key":   proposed.StorageKey,
		"first_seen_at": time.Unix(1, 0).UTC(),
	}, proposed)
	if different.NeedsSettlement || different.Result.Outcome != StartBlockDeleteOrphanDifferentTarget {
		t.Fatalf("usable different-target CAS = settlement:%v outcome:%s, want different_target", different.NeedsSettlement, different.Result.Outcome)
	}
}

func TestP4B_SettledOrphanClassification(t *testing.T) {
	proposed := BlockDeleteTarget{StorageClass: "hot", StorageKey: "blocks/org/aa/bb/key"}
	row := s3OrphanCASRow{Target: proposed, FirstSeenAt: time.Unix(2, 0).UTC()}

	absent := resultFromSettledS3Orphan(s3OrphanCASRow{}, false, nil, proposed, errors.New("lwt timeout"))
	if absent.Outcome != StartBlockDeleteOrphanNotPublished {
		t.Fatalf("SERIAL absence = %s, want not_published", absent.Outcome)
	}

	same := resultFromSettledS3Orphan(row, true, nil, proposed, errors.New("lwt timeout"))
	if same.Outcome != StartBlockDeleteOrphanSameTarget || !same.FirstSeenAt.Equal(row.FirstSeenAt) {
		t.Fatalf("SERIAL same-target = %+v, want same_target at stored token", same)
	}

	different := resultFromSettledS3Orphan(s3OrphanCASRow{
		Target:      BlockDeleteTarget{StorageClass: "cold", StorageKey: proposed.StorageKey},
		FirstSeenAt: row.FirstSeenAt,
	}, true, nil, proposed, nil)
	if different.Outcome != StartBlockDeleteOrphanDifferentTarget {
		t.Fatalf("SERIAL different-target = %s, want different_target", different.Outcome)
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
	proposed := BlockDeleteTarget{StorageClass: "hot", StorageKey: "blocks/org/aa/bb/key"}
	token := time.Unix(3, 0).UTC()
	prior := StartBlockDeleteOrphanResult{Outcome: StartBlockDeleteOrphanSameTarget, FirstSeenAt: token, ExistingTarget: proposed, Submitted: true}

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

	visible := classifyCanonicalOrphanVisibility(S3OrphanInfo{
		StorageClass:  proposed.StorageClass,
		StorageKey:    proposed.StorageKey,
		FirstSeenAt:   token,
		RecoveryPhase: S3OrphanPhasePendingS3,
	}, true, nil, proposed, token, prior)
	if visible.Outcome != StartBlockDeleteOrphanSameTarget {
		t.Fatalf("matching EACH_QUORUM row = %s, want same_target", visible.Outcome)
	}

	other := classifyCanonicalOrphanVisibility(S3OrphanInfo{
		StorageClass:  "cold",
		StorageKey:    proposed.StorageKey,
		FirstSeenAt:   token,
		RecoveryPhase: S3OrphanPhasePendingS3,
	}, true, nil, proposed, token, prior)
	if other.Outcome != StartBlockDeleteOrphanDifferentTarget {
		t.Fatalf("visible different target = %s, want different_target", other.Outcome)
	}

	mismatchedToken := classifyCanonicalOrphanVisibility(S3OrphanInfo{
		StorageClass:  proposed.StorageClass,
		StorageKey:    proposed.StorageKey,
		FirstSeenAt:   token.Add(time.Second),
		RecoveryPhase: S3OrphanPhasePendingS3,
	}, true, nil, proposed, token, prior)
	if mismatchedToken.Outcome != StartBlockDeleteOrphanInvalid {
		t.Fatalf("EACH_QUORUM first_seen_at mismatch = %s, want invalid", mismatchedToken.Outcome)
	}

	incomplete := classifyCanonicalOrphanVisibility(S3OrphanInfo{
		StorageClass: proposed.StorageClass,
		StorageKey:   "",
		FirstSeenAt:  token,
	}, true, nil, proposed, token, prior)
	if incomplete.Outcome != StartBlockDeleteOrphanInvalid {
		t.Fatalf("EACH_QUORUM incomplete identity = %s, want invalid", incomplete.Outcome)
	}

	advanced := classifyCanonicalOrphanVisibility(S3OrphanInfo{
		StorageClass:  proposed.StorageClass,
		StorageKey:    proposed.StorageKey,
		FirstSeenAt:   token,
		RecoveryPhase: S3OrphanPhasePendingMappingCleanup,
	}, true, nil, proposed, token, prior)
	if advanced.Outcome != StartBlockDeleteOrphanLifecycleAdvanced {
		t.Fatalf("pending_mapping_cleanup must not authorize finalize: got %s", advanced.Outcome)
	}

	emptyPhase := classifyCanonicalOrphanVisibility(S3OrphanInfo{
		StorageClass: proposed.StorageClass,
		StorageKey:   proposed.StorageKey,
		FirstSeenAt:  token,
	}, true, nil, proposed, token, prior)
	if emptyPhase.Outcome != StartBlockDeleteOrphanLifecycleAdvanced {
		t.Fatalf("empty recovery_phase must not authorize finalize: got %s", emptyPhase.Outcome)
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

func TestP4B_WorkerDifferentTargetReleasesClaimWithoutRetry(t *testing.T) {
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
	if block.GCState != "" || block.GCClaimID != "" {
		t.Fatalf("different target must release this claim, got state=%q claim=%q", block.GCState, block.GCClaimID)
	}
	items := store.QueueItems(orgID)
	if len(items) != 1 || items[0].RetryCount != 0 {
		t.Fatalf("queue after different target = %+v, want the same item with retry_count=0", items)
	}
	if store.QueueRequeueCallsForTest() != 1 || store.QueueCompleteCallsForTest() != 0 || store.QueueFailCallsForTest() != 0 {
		t.Fatalf("queue lifecycle calls = complete:%d requeue:%d fail:%d, want 0:1:0", store.QueueCompleteCallsForTest(), store.QueueRequeueCallsForTest(), store.QueueFailCallsForTest())
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

func TestP4B_WorkerOrphanRefusalNotOwnerLeavesQueueUntouched(t *testing.T) {
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

			store.SetReleaseBlockClaimHookForTest(func() {
				store.SeedBlockClaimForTest(orgID, blockID, "foreign-owner", time.Now().UTC())
			})
			if n, err := w.ProcessOnce(context.Background()); err != nil || n != 0 {
				t.Fatalf("ProcessOnce() = (%d, %v), want untouched late-loser refusal", n, err)
			}

			block := store.GetBlock(orgID, blockID)
			if block == nil || block.GCState != "deleting" || block.GCClaimID != "foreign-owner" {
				t.Fatalf("foreign claim was not preserved: block=%+v", block)
			}
			items := store.QueueItems(orgID)
			if len(items) != 1 || items[0].RetryCount != originalItem.RetryCount || items[0].QueuedAt != originalItem.QueuedAt || items[0].IdentityAt != originalItem.IdentityAt || items[0].BlockGCCandidateIdentity != originalItem.BlockGCCandidateIdentity {
				t.Fatalf("queue after not-owner release = %+v, want exact original item %+v", items, originalItem)
			}
			if store.QueueCompleteCallsForTest() != 0 || store.QueueRequeueCallsForTest() != 0 || store.QueueFailCallsForTest() != 0 {
				t.Fatalf("queue lifecycle calls = complete:%d requeue:%d fail:%d, want all zero", store.QueueCompleteCallsForTest(), store.QueueRequeueCallsForTest(), store.QueueFailCallsForTest())
			}
			if _, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, candidate.Identity()); err != nil || !ok {
				t.Fatalf("candidate was consumed after not-owner release: ok=%v err=%v", ok, err)
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
		{
			name: "canonical visibility unconfirmed",
			configure: func(store *MockStore, orgID uuid.UUID, blockID string, candidateAt time.Time) {
				seedS3Orphan(t, store, orgID, blockID, "hot", "sha1-existing", "", candidateAt)
				store.SetStartBlockDeleteOrphanCanonicalUnconfirmedOnceForTest()
			},
		},
		{
			name: "lifecycle advanced",
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
