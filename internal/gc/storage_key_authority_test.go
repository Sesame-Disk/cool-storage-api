package gc

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The persisted storage_key says WHICH object to destroy, and a BlockStore is only
// a bucket client: it deletes whatever key it is handed, prefix and all. So a row
// carrying another org's key would aim this org's delete at that org's bytes —
// the cross-org delete P10 closed in code, reopened through data. These tests pin
// the refusal on both destructive paths, and they assert the *absence* of a
// delete, because the only observable that matters here is that nothing happened.

func TestWorker_ProcessBlock_RefusesStorageKeyFromAnotherOrg(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})

	orgID := uuid.New()
	victimOrgID := uuid.New()
	const blockID = "blk-foreign-key"
	store.AddOrganization(orgID)
	store.AddBlock(orgID, blockID, "hot", 0)
	store.SetBlockStorageKeyForTest(orgID, blockID, MockCanonicalStorageKey(victimOrgID.String(), blockID))

	queuedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	if err := store.EnqueueItem(orgID, queuedAt, ItemBlock, blockID, uuid.Nil, "hot", 0); err != nil {
		t.Fatalf("EnqueueItem() error = %v", err)
	}

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce() error = %v", err)
	}

	if got := sp.ScopedBlockDeletes(); len(got) != 0 {
		t.Fatalf("S3 must not be touched for a foreign storage key, got %+v", got)
	}
	block := store.GetBlock(orgID, blockID)
	if block == nil {
		t.Fatal("canonical row must survive a refused delete")
	}
	if block.GCState != "" || block.GCClaimID != "" {
		t.Fatalf("claim must be released, got state=%q claim=%q", block.GCState, block.GCClaimID)
	}
	// Aborting before StartBlockDeleteOrphan is the point: a recovery row would
	// hand the same unverified key to RecoverS3Orphans on a later sweep.
	if got := store.AllS3Orphans(); len(got) != 0 {
		t.Fatalf("no recovery row may be recorded for a refused delete, got %+v", got)
	}
}

func TestWorker_ProcessBlock_RefusesEmptyStorageKey(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})

	orgID := uuid.New()
	const blockID = "blk-empty-key"
	store.AddOrganization(orgID)
	store.AddBlock(orgID, blockID, "hot", 0)
	store.SetBlockStorageKeyForTest(orgID, blockID, "   ")

	queuedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	if err := store.EnqueueItem(orgID, queuedAt, ItemBlock, blockID, uuid.Nil, "hot", 0); err != nil {
		t.Fatalf("EnqueueItem() error = %v", err)
	}

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce() error = %v", err)
	}

	if got := sp.ScopedBlockDeletes(); len(got) != 0 {
		t.Fatalf("S3 must not be touched without a canonical locator, got %+v", got)
	}
	block := store.GetBlock(orgID, blockID)
	if block == nil {
		t.Fatal("canonical row must survive a refused delete")
	}
	if block.GCState != "" || block.GCClaimID != "" {
		t.Fatalf("claim must be released, got state=%q claim=%q", block.GCState, block.GCClaimID)
	}
	if got := store.AllS3Orphans(); len(got) != 0 {
		t.Fatalf("no recovery row may be recorded for a refused delete, got %+v", got)
	}
}

func TestWorker_RecoverS3Orphans_RefusesStorageKeyFromAnotherOrg(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})

	orgID := uuid.New()
	victimOrgID := uuid.New()
	const blockID = "orph-foreign-key"
	if _, err := store.StartBlockDeleteOrphan(orgID, blockID, "hot", MockCanonicalStorageKey(victimOrgID.String(), blockID), "", time.Now().UTC()); err != nil {
		t.Fatalf("StartBlockDeleteOrphan: %v", err)
	}

	recovered, err := w.RecoverS3Orphans(context.Background(), 100)
	if err == nil {
		t.Fatal("RecoverS3Orphans() error = nil, want storage key mismatch error")
	}
	if recovered != 0 {
		t.Fatalf("recovered = %d, want 0", recovered)
	}
	if got := sp.ScopedBlockDeletes(); len(got) != 0 {
		t.Fatalf("S3 must not be touched for a foreign storage key, got %+v", got)
	}
	if store.S3OrphanCount() != 1 {
		t.Fatal("orphan row must remain for repair rather than be consumed by a refused delete")
	}
}

func TestWorker_RecoverS3Orphans_RefusesEmptyStorageKey(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})

	orgID := uuid.New()
	const blockID = "orph-empty-key"
	seedS3Orphan(t, store, orgID, blockID, "hot", "", "earlier failure", time.Now())
	store.SetS3OrphanStorageKeyForTest(orgID, blockID, "  ")

	recovered, err := w.RecoverS3Orphans(context.Background(), 100)
	if err == nil {
		t.Fatal("RecoverS3Orphans() error = nil, want empty storage key error")
	}
	if recovered != 0 {
		t.Fatalf("recovered = %d, want 0", recovered)
	}
	if got := sp.BlockStoreRequests(); len(got) != 0 {
		t.Fatalf("storage must not even be resolved without a locator, got %+v", got)
	}
	if got := sp.ScopedBlockDeletes(); len(got) != 0 {
		t.Fatalf("S3 must not be touched without a locator, got %+v", got)
	}
	if store.S3OrphanCount() != 1 {
		t.Fatal("orphan row must remain for repair")
	}
}

// The recovery row is what a later sweep hands to S3, so refusing to create one
// without a locator is what keeps the empty-key case from becoming a delete with
// a key invented downstream.
func TestStartBlockDeleteOrphanRequiresStorageKey(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()

	if _, err := store.StartBlockDeleteOrphan(orgID, "blk-no-key", "hot", "   ", "", time.Now().UTC()); err == nil {
		t.Fatal("StartBlockDeleteOrphan() error = nil, want missing storage key error")
	}
	if store.S3OrphanCount() != 0 {
		t.Fatalf("no orphan row may be created without a storage key, got %d", store.S3OrphanCount())
	}
}
