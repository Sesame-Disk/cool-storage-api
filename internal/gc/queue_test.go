package gc

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestItemType_Constants(t *testing.T) {
	tests := []struct {
		itemType ItemType
		want     string
	}{
		{ItemBlock, "block"},
		{ItemCommit, "commit"},
		{ItemFSObject, "fs_object"},
		{ItemBlockMapping, "block_mapping"},
		{ItemShareLink, "share_link"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if string(tt.itemType) != tt.want {
				t.Errorf("ItemType = %q, want %q", string(tt.itemType), tt.want)
			}
		})
	}
}

func TestQueueItem_Fields(t *testing.T) {
	orgID := uuid.New()
	libID := uuid.New()

	item := QueueItem{
		OrgID:        orgID,
		ItemType:     ItemBlock,
		ItemID:       "abc123def456",
		LibraryID:    libID,
		StorageClass: "hot",
		RetryCount:   0,
	}

	if item.OrgID != orgID {
		t.Errorf("OrgID = %v, want %v", item.OrgID, orgID)
	}
	if item.ItemType != ItemBlock {
		t.Errorf("ItemType = %v, want %v", item.ItemType, ItemBlock)
	}
	if item.ItemID != "abc123def456" {
		t.Errorf("ItemID = %v, want abc123def456", item.ItemID)
	}
}

func TestNewQueue_WithMockStore(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)
	if q == nil {
		t.Fatal("NewQueue returned nil")
	}
}

func TestQueue_EnqueueAndDequeue(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)

	orgID := uuid.New()
	libID := uuid.New()

	// Enqueue an item
	err := q.Enqueue(orgID, ItemBlock, "block-1", libID, "hot")
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	// DequeueBatch with 0 grace period should return the item
	items, err := q.DequeueBatch(orgID, 10, 0)
	if err != nil {
		t.Fatalf("DequeueBatch failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].ItemID != "block-1" {
		t.Errorf("ItemID = %q, want block-1", items[0].ItemID)
	}
	if items[0].ItemType != ItemBlock {
		t.Errorf("ItemType = %v, want %v", items[0].ItemType, ItemBlock)
	}
	if items[0].StorageClass != "hot" {
		t.Errorf("StorageClass = %q, want hot", items[0].StorageClass)
	}

	// Complete the item
	err = q.Complete(orgID, items[0].QueuedAt, items[0].ItemType, items[0].ItemID)
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	// DequeueBatch should now return empty
	items, err = q.DequeueBatch(orgID, 10, 0)
	if err != nil {
		t.Fatalf("DequeueBatch failed: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items after complete, got %d", len(items))
	}
}

func TestQueue_DequeueBatch_GracePeriod(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)

	orgID := uuid.New()

	// Enqueue an item (queued at time.Now())
	err := q.Enqueue(orgID, ItemBlock, "block-1", uuid.Nil, "hot")
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	// DequeueBatch with 1h grace period should return empty (item too new)
	items, err := q.DequeueBatch(orgID, 10, 1*time.Hour)
	if err != nil {
		t.Fatalf("DequeueBatch failed: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items with 1h grace period, got %d", len(items))
	}

	// DequeueBatch with 0 grace period should return the item
	items, err = q.DequeueBatch(orgID, 10, 0)
	if err != nil {
		t.Fatalf("DequeueBatch failed: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("expected 1 item with 0 grace period, got %d", len(items))
	}
}

func TestQueue_IncrementRetry(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)

	orgID := uuid.New()

	err := q.Enqueue(orgID, ItemBlock, "block-1", uuid.Nil, "hot")
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	// Get item
	items, _ := q.DequeueBatch(orgID, 10, 0)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	if items[0].RetryCount != 0 {
		t.Errorf("initial RetryCount = %d, want 0", items[0].RetryCount)
	}

	// Increment retry
	err = q.IncrementRetry(items[0])
	if err != nil {
		t.Fatalf("IncrementRetry failed: %v", err)
	}

	// Re-fetch and verify
	items, _ = q.DequeueBatch(orgID, 10, 0)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].RetryCount != 1 {
		t.Errorf("RetryCount = %d after increment, want 1", items[0].RetryCount)
	}

	// Increment again
	q.IncrementRetry(items[0])
	items, _ = q.DequeueBatch(orgID, 10, 0)
	if items[0].RetryCount != 2 {
		t.Errorf("RetryCount = %d after 2nd increment, want 2", items[0].RetryCount)
	}
}

func TestQueue_ListOrgsWithQueuedItems(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)

	org1 := uuid.New()
	org2 := uuid.New()

	// Enqueue items for two orgs
	q.Enqueue(org1, ItemBlock, "b1", uuid.Nil, "hot")
	q.Enqueue(org2, ItemCommit, "c1", uuid.New(), "")

	orgs, err := q.ListOrgsWithQueuedItems()
	if err != nil {
		t.Fatalf("ListOrgsWithQueuedItems failed: %v", err)
	}
	if len(orgs) != 2 {
		t.Errorf("expected 2 orgs, got %d", len(orgs))
	}

	// Complete all items for org1
	items, _ := q.DequeueBatch(org1, 10, 0)
	for _, item := range items {
		q.Complete(org1, item.QueuedAt, item.ItemType, item.ItemID)
	}

	orgs, _ = q.ListOrgsWithQueuedItems()
	if len(orgs) != 1 {
		t.Errorf("expected 1 org after completing org1 items, got %d", len(orgs))
	}
}

func TestQueue_GetQueueSize(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)

	orgID := uuid.New()

	// Empty queue
	size, err := q.GetQueueSize(orgID)
	if err != nil {
		t.Fatalf("GetQueueSize failed: %v", err)
	}
	if size != 0 {
		t.Errorf("expected size 0, got %d", size)
	}

	// Enqueue 3 items
	q.Enqueue(orgID, ItemBlock, "b1", uuid.Nil, "hot")
	q.Enqueue(orgID, ItemBlock, "b2", uuid.Nil, "hot")
	q.Enqueue(orgID, ItemCommit, "c1", uuid.New(), "")

	size, _ = q.GetQueueSize(orgID)
	if size != 3 {
		t.Errorf("expected size 3, got %d", size)
	}

	// Complete 1 item
	items, _ := q.DequeueBatch(orgID, 1, 0)
	q.Complete(orgID, items[0].QueuedAt, items[0].ItemType, items[0].ItemID)

	size, _ = q.GetQueueSize(orgID)
	if size != 2 {
		t.Errorf("expected size 2 after complete, got %d", size)
	}
}

func TestQueue_GetTotalQueueSize(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)

	org1 := uuid.New()
	org2 := uuid.New()

	q.Enqueue(org1, ItemBlock, "b1", uuid.Nil, "hot")
	q.Enqueue(org1, ItemBlock, "b2", uuid.Nil, "hot")
	q.Enqueue(org2, ItemCommit, "c1", uuid.New(), "")

	total, err := q.GetTotalQueueSize()
	if err != nil {
		t.Fatalf("GetTotalQueueSize failed: %v", err)
	}
	if total != 3 {
		t.Errorf("expected total 3, got %d", total)
	}
}

func TestQueue_MultipleItemTypes(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)

	orgID := uuid.New()
	libID := uuid.New()

	q.Enqueue(orgID, ItemBlock, "block-1", uuid.Nil, "hot")
	q.Enqueue(orgID, ItemCommit, "commit-1", libID, "")
	q.Enqueue(orgID, ItemFSObject, "fs-1", libID, "")
	q.Enqueue(orgID, ItemShareLink, "token-abc", uuid.Nil, "")
	q.Enqueue(orgID, ItemBlockMapping, "ext-123", uuid.Nil, "")

	items, err := q.DequeueBatch(orgID, 10, 0)
	if err != nil {
		t.Fatalf("DequeueBatch failed: %v", err)
	}
	if len(items) != 5 {
		t.Errorf("expected 5 items, got %d", len(items))
	}

	// Verify all types are present
	typeSet := make(map[ItemType]bool)
	for _, item := range items {
		typeSet[item.ItemType] = true
	}
	for _, expected := range []ItemType{ItemBlock, ItemCommit, ItemFSObject, ItemShareLink, ItemBlockMapping} {
		if !typeSet[expected] {
			t.Errorf("missing item type %s", expected)
		}
	}
}

func TestQueue_NewItemTypes(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)

	orgID := uuid.New()
	libID := uuid.New()

	// Enqueue the new item types: share and restore_job
	err := q.Enqueue(orgID, ItemShare, "share-1", libID, "")
	if err != nil {
		t.Fatalf("Enqueue ItemShare failed: %v", err)
	}
	err = q.Enqueue(orgID, ItemRestoreJob, "restore-job-1", libID, "")
	if err != nil {
		t.Fatalf("Enqueue ItemRestoreJob failed: %v", err)
	}

	items, err := q.DequeueBatch(orgID, 10, 0)
	if err != nil {
		t.Fatalf("DequeueBatch failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	typeSet := make(map[ItemType]bool)
	for _, item := range items {
		typeSet[item.ItemType] = true
	}
	if !typeSet[ItemShare] {
		t.Error("missing ItemShare")
	}
	if !typeSet[ItemRestoreJob] {
		t.Error("missing ItemRestoreJob")
	}
}

func TestItemType_NewConstants(t *testing.T) {
	if string(ItemShare) != "share" {
		t.Errorf("ItemShare = %q, want %q", string(ItemShare), "share")
	}
	if string(ItemRestoreJob) != "restore_job" {
		t.Errorf("ItemRestoreJob = %q, want %q", string(ItemRestoreJob), "restore_job")
	}
}
