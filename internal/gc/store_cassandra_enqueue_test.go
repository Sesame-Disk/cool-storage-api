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

// TestParseStoredQueueLibraryID covers the library_id round-trip used by the
// Cassandra DLQ requeue path: empty stays empty, valid UUIDs canonicalize, the
// nil UUID (carried by library-less org/user cascades) is accepted, and a
// non-empty non-UUID is rejected.
func TestParseStoredQueueLibraryID(t *testing.T) {
	realID := uuid.New()

	for _, tc := range []struct {
		name         string
		raw          string
		wantID       uuid.UUID
		wantQueue    string
		wantErr      bool
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
