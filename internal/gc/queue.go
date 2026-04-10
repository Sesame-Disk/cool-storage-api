package gc

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ItemType identifies the kind of object in the GC queue
type ItemType string

const (
	ItemBlock          ItemType = "block"
	ItemCommit         ItemType = "commit"
	ItemFSObject       ItemType = "fs_object"
	ItemBlockMapping   ItemType = "block_mapping"
	ItemShareLink      ItemType = "share_link"
	ItemUserCascade    ItemType = "user_cascade"
	ItemLibraryCascade ItemType = "library_cascade"
	ItemOrgCascade     ItemType = "org_cascade"
)

// QueueItem represents a single item pending garbage collection
type QueueItem struct {
	OrgID        uuid.UUID
	QueuedAt     time.Time
	ItemType     ItemType
	ItemID       string
	LibraryID    uuid.UUID
	StorageClass string
	RetryCount   int
}

// Queue provides operations for the gc_queue.
type Queue struct {
	store GCStore
}

// NewQueue creates a new Queue instance.
func NewQueue(store GCStore) *Queue {
	return &Queue{store: store}
}

// Enqueue inserts an item into the gc_queue for later deletion.
func (q *Queue) Enqueue(orgID uuid.UUID, itemType ItemType, itemID string, libraryID uuid.UUID, storageClass string) error {
	return q.store.EnqueueItem(orgID, time.Now(), itemType, itemID, libraryID, storageClass, 0)
}

// EnqueueBatch inserts multiple items into the gc_queue efficiently.
func (q *Queue) EnqueueBatch(items []QueueItem) error {
	if len(items) == 0 {
		return nil
	}
	return q.store.EnqueueBatch(items)
}

// DequeueBatch retrieves the oldest items from the queue for a given org
// that are older than minAge (grace period). Returns up to batchSize items.
func (q *Queue) DequeueBatch(orgID uuid.UUID, batchSize int, minAge time.Duration) ([]QueueItem, error) {
	cutoff := time.Now().Add(-minAge)
	return q.store.DequeueBatch(orgID, batchSize, cutoff)
}

// Complete removes a processed item from the gc_queue.
func (q *Queue) Complete(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string) error {
	return q.store.CompleteItem(orgID, queuedAt, itemType, itemID)
}

// IncrementRetry updates the retry count for a failed item and requeues it at the back of the queue.
func (q *Queue) IncrementRetry(item QueueItem) error {
	newQueuedAt := time.Now()
	return q.store.RequeueItem(item.OrgID, item.QueuedAt, newQueuedAt, item.ItemType, item.ItemID, item.LibraryID, item.StorageClass, item.RetryCount+1)
}

// GetQueueSize returns the approximate number of items in the queue for an org.
func (q *Queue) GetQueueSize(orgID uuid.UUID) (int, error) {
	return q.store.GetQueueSize(orgID)
}

// GetTotalQueueSize returns the approximate total number of items across all orgs.
func (q *Queue) GetTotalQueueSize() (int, error) {
	return q.store.GetTotalQueueSize()
}

// ListOrgsWithQueuedItems returns org_ids that have items in the gc_queue.
func (q *Queue) ListOrgsWithQueuedItems() ([]uuid.UUID, error) {
	orgs, err := q.store.ListOrgsWithQueuedItems()
	if err != nil {
		return nil, fmt.Errorf("failed to list orgs: %w", err)
	}
	return orgs, nil
}
