package gc

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestCassandraStore_EnqueueItem_RejectsRepresentationRequiredTypes pins the
// production guard on the raw single-row path without a live Cassandra: the
// representation check runs before s.db is ever touched, so a zero-value
// CassandraStore is enough to prove the reference-carrying item types are
// rejected there (mirroring MockStore) and cannot smuggle an incomplete row
// past the block-representation contract.
func TestCassandraStore_EnqueueItem_RejectsRepresentationRequiredTypes(t *testing.T) {
	store := &CassandraStore{} // nil db: guard must return before any query

	for _, itemType := range []ItemType{ItemCommit, ItemFSObject, ItemLibraryCascade} {
		t.Run(string(itemType), func(t *testing.T) {
			err := store.EnqueueItem(uuid.New(), time.Now(), itemType, "item-1", uuid.New(), "hot", 0)
			if err == nil {
				t.Fatalf("EnqueueItem(%s) = nil, want rejection", itemType)
			}
		})
	}
}
