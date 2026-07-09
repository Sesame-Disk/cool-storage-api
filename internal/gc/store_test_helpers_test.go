package gc

import (
	"time"

	"github.com/google/uuid"
)

// SeedQueueItemForTest injects a raw gc_queue row for test setup, deliberately
// bypassing the block-representation enqueue contract that EnqueueItem and
// EnqueueBatch enforce. It always stores an empty block representation, so it is
// how unit tests stage the incomplete/legacy commit/fs_object/library_cascade
// rows those guarded paths now reject — without building full library fixtures.
//
// It lives in a _test.go file on purpose: that keeps the bypass out of the
// production build entirely (an external caller of MockStore cannot reach it),
// so the enqueue invariant is a structural restriction, not just a naming
// convention.
func (m *MockStore) SeedQueueItemForTest(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string, libraryID uuid.UUID, storageClass string, retryCount int) {
	m.seedQueueItemRow(orgID, queuedAt, itemType, itemID, libraryID, storageClass, retryCount)
}
