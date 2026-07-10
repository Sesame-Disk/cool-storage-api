package gc

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var errInvalidLibraryGuardMode = errors.New("invalid library guard mode")

// ItemType identifies the kind of object in the GC queue
type ItemType string

const (
	ItemBlock          ItemType = "block"
	ItemCommit         ItemType = "commit"
	ItemFSObject       ItemType = "fs_object"
	ItemShareLink      ItemType = "share_link"
	ItemUserCascade    ItemType = "user_cascade"
	ItemLibraryCascade ItemType = "library_cascade"
	ItemOrgCascade     ItemType = "org_cascade"
)

// LibraryGuardMode identifies the condition that must remain true before a
// queued commit or fs_object can be deleted.
type LibraryGuardMode string

const (
	LibraryGuardNone                  LibraryGuardMode = ""
	LibraryGuardDeletedAtIdentity     LibraryGuardMode = "deleted_at_identity"
	LibraryGuardCanonicalMustBeAbsent LibraryGuardMode = "canonical_absent"
)

// QueueItem represents a single item pending garbage collection
type QueueItem struct {
	OrgID                       uuid.UUID
	QueuedAt                    time.Time
	IdentityAt                  time.Time
	RequiresLibraryDeletedCheck bool
	LibraryGuardMode            LibraryGuardMode
	ItemType                    ItemType
	ItemID                      string
	LibraryID                   uuid.UUID
	BlockRepresentationID       string
	StorageClass                string
	RetryCount                  int
}

func effectiveIdentityAt(queuedAt, identityAt time.Time) time.Time {
	if identityAt.IsZero() {
		return queuedAt
	}
	return identityAt
}

func effectiveLibraryGuardMode(mode LibraryGuardMode, requiresLibraryDeletedCheck bool) LibraryGuardMode {
	if mode != LibraryGuardNone {
		return mode
	}
	if requiresLibraryDeletedCheck {
		return LibraryGuardDeletedAtIdentity
	}
	return LibraryGuardNone
}

func validateLibraryGuardMode(mode LibraryGuardMode, requiresLibraryDeletedCheck bool) (LibraryGuardMode, error) {
	effective := effectiveLibraryGuardMode(mode, requiresLibraryDeletedCheck)
	switch effective {
	case LibraryGuardNone, LibraryGuardDeletedAtIdentity, LibraryGuardCanonicalMustBeAbsent:
		return effective, nil
	default:
		return LibraryGuardNone, fmt.Errorf("%w %q", errInvalidLibraryGuardMode, effective)
	}
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
	if itemTypeRequiresBlockRepresentation(itemType) {
		return fmt.Errorf("item type %s requires explicit block representation; use EnqueueBatch", itemType)
	}
	return q.store.EnqueueItem(orgID, time.Now(), itemType, itemID, libraryID, storageClass, 0)
}

// EnqueueCascade inserts a cascade-generated item into the gc_queue using the
// parent's QueuedAt timestamp. Since the parent already passed the grace period,
// cascade children become immediately eligible for processing — they are known
// to be unreferenced (the parent object is being deleted).
func (q *Queue) EnqueueCascade(orgID uuid.UUID, parentQueuedAt time.Time, itemType ItemType, itemID string, libraryID uuid.UUID, storageClass string) error {
	if itemTypeRequiresBlockRepresentation(itemType) {
		return fmt.Errorf("item type %s requires explicit block representation; use EnqueueBatch", itemType)
	}
	return q.store.EnqueueItem(orgID, parentQueuedAt, itemType, itemID, libraryID, storageClass, 0)
}

// EnqueueBatch inserts multiple items into the gc_queue efficiently.
//
// EnqueueBatch is the real choke point every producer uses, so the block
// representation invariant is enforced here (not only in Enqueue/EnqueueCascade):
// commits, fs_objects and library cascades must carry a *canonical*
// BlockRepresentationID. Rejecting an empty OR malformed value keeps an
// incomplete task — which the worker would fail to map and then retry forever,
// leaking references/blocks/mappings — from ever reaching gc_queue.
func (q *Queue) EnqueueBatch(items []QueueItem) error {
	if len(items) == 0 {
		return nil
	}
	for _, item := range items {
		if _, err := validateLibraryGuardMode(item.LibraryGuardMode, item.RequiresLibraryDeletedCheck); err != nil {
			return fmt.Errorf("invalid guard for %s/%s: %w", item.ItemType, item.ItemID, err)
		}
		if err := validateQueueItemBlockRepresentation(item); err != nil {
			return err
		}
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
	return q.store.RequeueItem(item.OrgID, item.QueuedAt, newQueuedAt, item.ItemType, item.ItemID, item.LibraryID, item.BlockRepresentationID, item.StorageClass, item.RetryCount+1, effectiveIdentityAt(item.QueuedAt, item.IdentityAt), item.RequiresLibraryDeletedCheck, item.LibraryGuardMode)
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
