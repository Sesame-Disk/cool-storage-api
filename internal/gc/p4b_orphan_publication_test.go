package gc

import (
	"bytes"
	"context"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestP4B_StartBlockDeleteOrphanSourceContract(t *testing.T) {
	source, err := os.ReadFile("store_cassandra.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "store_cassandra.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	function := findGCFunction(file, "StartBlockDeleteOrphan")
	if function == nil {
		t.Fatal("StartBlockDeleteOrphan not found")
	}
	var formatted bytes.Buffer
	if err := format.Node(&formatted, token.NewFileSet(), function); err != nil {
		t.Fatalf("format StartBlockDeleteOrphan: %v", err)
	}
	text := formatted.String()

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
	if got := strings.Count(text, "ensureS3OrphanProjectionResult"); got != 3 {
		t.Fatalf("StartBlockDeleteOrphan projection wrapper calls = %d, want 3 guarded return paths", got)
	}

	settlement := findGCFunction(file, "settleS3OrphanState")
	if settlement == nil {
		t.Fatal("settleS3OrphanState not found")
	}
	if !gcQueryMethodHas(settlement, "SELECT storage_class, storage_key, first_seen_at", "Consistency", "Serial") {
		t.Fatal("settleS3OrphanState must read the canonical row at Consistency(gocql.Serial)")
	}
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
