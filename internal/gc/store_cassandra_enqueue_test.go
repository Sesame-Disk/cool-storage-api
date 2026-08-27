package gc

import (
	"strings"
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
			if err == nil || !strings.Contains(err.Error(), "use EnqueueBatch") {
				t.Fatalf("EnqueueItem(%s) error = %v, want raw-path guard mentioning use EnqueueBatch", itemType, err)
			}
		})
	}
}

// TestEnqueueItemRefusesBlockItems pins the R26 half of the raw-path guard.
//
// ItemBlock is refused for a different reason than the representation-carrying
// types, and it is the more important one: a block work item is legitimate only
// when a zero-ref DECISION has already produced a candidate for an exact
// P = (storage_class, storage_key), and this single-row path has no P to carry.
// An earlier revision papered over that by calling EnsureBlockGCCandidate from
// inside EnqueueItem, which made "enqueue" mint destructive authority out of
// nothing — the inverse of the rule the whole slice rests on. Both the raw store
// path and the Queue wrapper must send producers to EnqueueBatch instead.
func TestEnqueueItemRefusesBlockItems(t *testing.T) {
	t.Run("cassandra_raw_path", func(t *testing.T) {
		// nil db on purpose: the guard has to return BEFORE any query, so reaching
		// the session at all is itself the regression. Recovering turns that into a
		// readable assertion instead of a panic that says nothing about the rule.
		store := &CassandraStore{}
		var err error
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("EnqueueItem(ItemBlock) reached the database (%v); the guard must refuse a block item "+
						"before any query, because this path has no exact candidate identity to carry", recovered)
				}
			}()
			err = store.EnqueueItem(uuid.New(), time.Now(), ItemBlock, "block-1", uuid.Nil, "hot", 0)
		}()
		if err == nil || !strings.Contains(err.Error(), "use EnqueueBatch") {
			t.Fatalf("EnqueueItem(ItemBlock) error = %v, want a guard mentioning use EnqueueBatch", err)
		}
		if !strings.Contains(err.Error(), "candidate identity") {
			t.Fatalf("EnqueueItem(ItemBlock) error = %v, want it to say WHY: a block item needs an exact candidate identity", err)
		}
	})

	t.Run("mock_raw_path", func(t *testing.T) {
		store := NewMockStore()
		err := store.EnqueueItem(uuid.New(), time.Now(), ItemBlock, "block-1", uuid.Nil, "hot", 0)
		if err == nil || !strings.Contains(err.Error(), "EnqueueBatch") {
			t.Fatalf("MockStore.EnqueueItem(ItemBlock) error = %v, want the same guard as the Cassandra store", err)
		}
	})

	t.Run("queue_wrappers", func(t *testing.T) {
		queue := NewQueue(NewMockStore())
		if err := queue.Enqueue(uuid.New(), ItemBlock, "block-1", uuid.Nil, "hot"); err == nil {
			t.Fatal("Queue.Enqueue(ItemBlock) must refuse: it cannot carry a candidate identity")
		}
		if err := queue.EnqueueCascade(uuid.New(), time.Now(), ItemBlock, "block-1", uuid.Nil, "hot"); err == nil {
			t.Fatal("Queue.EnqueueCascade(ItemBlock) must refuse: it cannot carry a candidate identity")
		}
	})
}

// TestParseStoredQueueLibraryID covers the library_id round-trip used by the
// Cassandra DLQ requeue path: empty stays empty, valid UUIDs canonicalize, the
// nil UUID (carried by library-less org/user cascades) is accepted, and a
// non-empty non-UUID is rejected.
func TestParseStoredQueueLibraryID(t *testing.T) {
	realID := uuid.New()

	for _, tc := range []struct {
		name      string
		raw       string
		wantID    uuid.UUID
		wantQueue string
		wantErr   bool
	}{
		{name: "empty", raw: "", wantID: uuid.Nil, wantQueue: ""},
		{name: "whitespace_only", raw: "   ", wantID: uuid.Nil, wantQueue: ""},
		{name: "nil_uuid", raw: uuid.Nil.String(), wantID: uuid.Nil, wantQueue: uuid.Nil.String()},
		{name: "real_uuid", raw: realID.String(), wantID: realID, wantQueue: realID.String()},
		{name: "surrounding_spaces", raw: "  " + realID.String() + "  ", wantID: realID, wantQueue: realID.String()},
		{name: "uppercase_canonicalized", raw: strings.ToUpper(realID.String()), wantID: realID, wantQueue: realID.String()},
		{name: "garbage", raw: "not-a-uuid", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotID, gotQueue, err := parseStoredQueueLibraryID(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseStoredQueueLibraryID(%q) err = nil, want error", tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseStoredQueueLibraryID(%q) unexpected err = %v", tc.raw, err)
			}
			if gotID != tc.wantID {
				t.Fatalf("parseStoredQueueLibraryID(%q) id = %s, want %s", tc.raw, gotID, tc.wantID)
			}
			if gotQueue != tc.wantQueue {
				t.Fatalf("parseStoredQueueLibraryID(%q) queue value = %q, want %q", tc.raw, gotQueue, tc.wantQueue)
			}
		})
	}
}
