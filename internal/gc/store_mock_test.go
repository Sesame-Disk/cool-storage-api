package gc

import (
	"errors"
	"testing"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
)

func TestMockStore_GetLibraryBlockRepresentationID_RejectsOrgMismatchOnDeletedLibrary(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	otherOrgID := uuid.New()
	libID := uuid.New()

	store.AddDeletedLibrary(orgID, libID, "hot", time.Now().UTC())

	if _, err := store.GetLibraryBlockRepresentationID(otherOrgID, libID); !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("GetLibraryBlockRepresentationID error = %v, want %v", err, gocql.ErrNotFound)
	}
}

func TestMockStore_GetLibraryBlockRepresentationID_RejectsCrossDomainLiveLibrary(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	libID := uuid.New()
	store.AddLibrary(orgID, libID, "hot")
	store.mu.Lock()
	store.libraries[libID].Encrypted = false
	store.libraries[libID].BlockRepresentationID = db.EncryptedLibraryBlockRepresentationID(libID.String())
	store.mu.Unlock()

	if _, err := store.GetLibraryBlockRepresentationID(orgID, libID); err == nil {
		t.Fatal("GetLibraryBlockRepresentationID error = nil, want cross-domain rejection")
	}
}

// TestMockStore_GetLibraryBlockRepresentationID_ResolvesFromDeletedLibrary pins the
// Rama 3 durability contract: once migration 010 gives deleted_libraries a
// block_representation_id column, GetLibraryBlockRepresentationID resolves from
// the soft-deleted row when the live libraries row is already gone. A stored
// representation is returned as-is; an empty one still fails closed with
// gocql.ErrNotFound so callers fall back to the conservative dual-probe rather
// than resolving against a guessed representation.
func TestMockStore_GetLibraryBlockRepresentationID_ResolvesFromDeletedLibrary(t *testing.T) {
	orgID := uuid.New()

	t.Run("with representation returns it", func(t *testing.T) {
		store := NewMockStore()
		libID := uuid.New()
		store.deletedLibraries[libID] = &mockDeletedLibrary{
			OrgID:                 orgID,
			LibraryID:             libID,
			BlockRepresentationID: db.EncryptedLibraryBlockRepresentationID(libID.String()),
			StorageClass:          "hot",
			DeletedAt:             time.Now().UTC(),
		}

		got, err := store.GetLibraryBlockRepresentationID(orgID, libID)
		if err != nil {
			t.Fatalf("GetLibraryBlockRepresentationID: %v", err)
		}
		if want := db.EncryptedLibraryBlockRepresentationID(libID.String()); got != want {
			t.Fatalf("representation = %q, want %q", got, want)
		}
	})

	t.Run("empty representation fails closed", func(t *testing.T) {
		store := NewMockStore()
		libID := uuid.New()
		store.deletedLibraries[libID] = &mockDeletedLibrary{
			OrgID:        orgID,
			LibraryID:    libID,
			StorageClass: "hot",
			DeletedAt:    time.Now().UTC(),
		}

		if _, err := store.GetLibraryBlockRepresentationID(orgID, libID); !errors.Is(err, gocql.ErrNotFound) {
			t.Fatalf("GetLibraryBlockRepresentationID error = %v, want %v", err, gocql.ErrNotFound)
		}
	})
}

// TestMockStore_EnqueueBatchRejectsForeignRepresentation verifies the enqueue
// choke point applies the block-representation invariant directly at the store
// level (not just in the Queue wrapper): a commit item carrying a canonical
// representation that belongs to a *different* library must be rejected.
func TestMockStore_EnqueueBatchRejectsForeignRepresentation(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	libID := uuid.New()

	err := store.EnqueueBatch([]QueueItem{{
		OrgID:                 orgID,
		QueuedAt:              time.Now().UTC(),
		ItemType:              ItemCommit,
		ItemID:                "commit-1",
		LibraryID:             libID,
		BlockRepresentationID: db.EncryptedLibraryBlockRepresentationID(uuid.NewString()),
	}})
	if err == nil {
		t.Fatal("expected direct store enqueue to reject foreign representation")
	}
}

func TestMockStore_SoftDeleteLibrary_RejectsCrossDomainRepresentation(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	libID := uuid.New()
	store.AddLibrary(orgID, libID, "hot")
	store.mu.Lock()
	store.libraries[libID].Encrypted = true
	store.libraries[libID].BlockRepresentationID = db.PlainBlockRepresentationID
	store.mu.Unlock()

	if err := store.SoftDeleteLibrary(orgID, libID, uuid.Nil); err == nil {
		t.Fatal("SoftDeleteLibrary error = nil, want cross-domain rejection")
	}
	if deleted := store.deletedLibraries[libID]; deleted != nil {
		t.Fatalf("deleted library marker = %#v, want no marker on rejected soft delete", deleted)
	}
}

// TestMockStore_EnqueueItemRejectsRepresentationRequiredTypes verifies the raw
// single-row EnqueueItem path — which cannot carry a block representation — fails
// closed for the item types that require one, so a caller cannot bypass the
// EnqueueBatch invariant by writing straight to the store. Non-representation
// types (e.g. ItemBlock) still enqueue normally.
func TestMockStore_EnqueueItemRejectsRepresentationRequiredTypes(t *testing.T) {
	orgID := uuid.New()
	libID := uuid.New()

	for _, itemType := range []ItemType{ItemCommit, ItemFSObject, ItemLibraryCascade} {
		t.Run(string(itemType), func(t *testing.T) {
			store := NewMockStore()
			if err := store.EnqueueItem(orgID, time.Now().UTC(), itemType, "item-1", libID, "hot", 0); err == nil {
				t.Fatalf("EnqueueItem(%s) = nil, want rejection", itemType)
			}
		})
	}

	t.Run("ItemBlock still allowed", func(t *testing.T) {
		store := NewMockStore()
		if err := store.EnqueueItem(orgID, time.Now().UTC(), ItemBlock, "block-1", libID, "hot", 0); err != nil {
			t.Fatalf("EnqueueItem(ItemBlock) = %v, want nil", err)
		}
	})
}

// TestMockStore_ResolveBlockIDs_NoLiveLibraryUsesDualProbeFallback verifies that
// when the queue item carries no representation and the library has no live or
// soft-deleted row at all, ResolveBlockIDs falls back to probing both the
// plaintext and encrypted representations instead of erroring or silently
// leaving the SHA-1 unresolved. This is the plain-only leg of the dual-probe,
// which Rama 3 keeps as conservative protection (not the expected path).
func TestMockStore_ResolveBlockIDs_NoLiveLibraryUsesDualProbeFallback(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	libID := uuid.New()
	externalSHA1 := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	internalSHA256 := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	store.AddBlockMappingForRepresentation(orgID, db.PlainBlockRepresentationID, externalSHA1, internalSHA256)

	resolved, err := store.ResolveBlockIDs(orgID, libID, "", []string{externalSHA1})
	if err != nil {
		t.Fatalf("ResolveBlockIDs: %v", err)
	}
	if len(resolved) != 1 || resolved[0] != internalSHA256 {
		t.Fatalf("resolved = %v, want [%s]", resolved, internalSHA256)
	}
}

// TestMockStore_ResolveBlockIDs_DualProbeEncryptedOnlyResolves is the encrypted-only
// leg of the dual-probe: no live/soft-deleted library row, no plaintext mapping,
// only the library's encrypted representation has a forward mapping. ResolveBlockIDs
// must still resolve it instead of only ever trying plain:v1.
func TestMockStore_ResolveBlockIDs_DualProbeEncryptedOnlyResolves(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	libID := uuid.New()
	externalSHA1 := "cccccccccccccccccccccccccccccccccccccccc"
	internalSHA256 := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

	store.AddBlockMappingForRepresentation(orgID, db.EncryptedLibraryBlockRepresentationID(libID.String()), externalSHA1, internalSHA256)

	resolved, err := store.ResolveBlockIDs(orgID, libID, "", []string{externalSHA1})
	if err != nil {
		t.Fatalf("ResolveBlockIDs: %v", err)
	}
	if len(resolved) != 1 || resolved[0] != internalSHA256 {
		t.Fatalf("resolved = %v, want [%s]", resolved, internalSHA256)
	}
}

// TestMockStore_ResolveBlockIDs_DualProbeSameInternalResolvesWithoutAmbiguity covers
// the case where a plaintext library and an encrypted library both legitimately
// mapped the same external SHA-1 to the same internal SHA-256 (identical bytes
// stored under both representations). The dual-probe must resolve it directly
// and must NOT count it as ambiguous, since there is only one candidate answer.
func TestMockStore_ResolveBlockIDs_DualProbeSameInternalResolvesWithoutAmbiguity(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	libID := uuid.New()
	externalSHA1 := "ffffffffffffffffffffffffffffffffffffffff"
	internalSHA256 := "3333333333333333333333333333333333333333333333333333333333333333"

	store.AddBlockMappingForRepresentation(orgID, db.PlainBlockRepresentationID, externalSHA1, internalSHA256)
	store.AddBlockMappingForRepresentation(orgID, db.EncryptedLibraryBlockRepresentationID(libID.String()), externalSHA1, internalSHA256)

	beforeAmbiguous := testutil.ToFloat64(metrics.GCAuditEventsTotal.WithLabelValues("gc_block_mapping_unresolved_ambiguous_representation"))

	resolved, err := store.ResolveBlockIDs(orgID, libID, "", []string{externalSHA1})
	if err != nil {
		t.Fatalf("ResolveBlockIDs: %v", err)
	}
	if len(resolved) != 1 || resolved[0] != internalSHA256 {
		t.Fatalf("resolved = %v, want [%s]", resolved, internalSHA256)
	}

	afterAmbiguous := testutil.ToFloat64(metrics.GCAuditEventsTotal.WithLabelValues("gc_block_mapping_unresolved_ambiguous_representation"))
	if afterAmbiguous != beforeAmbiguous {
		t.Fatalf("ambiguous metric delta = %v, want 0 (same internal id on both sides is not ambiguous)", afterAmbiguous-beforeAmbiguous)
	}
}

// TestMockStore_ResolveBlockIDs_DualProbeAmbiguousLeavesUnresolvedAndCountsMetric
// covers the genuinely ambiguous case: plaintext and encrypted representations
// within the same org map the same external SHA-1 to different internal
// SHA-256 values. The resolver must refuse to guess — leave the id unresolved
// (never delete/rewrite the wrong reference) — and must record the ambiguity
// so it stays visible for drift/corruption alerting instead of a silent leak.
func TestMockStore_ResolveBlockIDs_DualProbeAmbiguousLeavesUnresolvedAndCountsMetric(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	libID := uuid.New()
	externalSHA1 := "9999999999999999999999999999999999999999"
	plainInternalSHA256 := "1111111111111111111111111111111111111111111111111111111111111111"
	encryptedInternalSHA256 := "2222222222222222222222222222222222222222222222222222222222222222"

	store.AddBlockMappingForRepresentation(orgID, db.PlainBlockRepresentationID, externalSHA1, plainInternalSHA256)
	store.AddBlockMappingForRepresentation(orgID, db.EncryptedLibraryBlockRepresentationID(libID.String()), externalSHA1, encryptedInternalSHA256)

	beforeAmbiguous := testutil.ToFloat64(metrics.GCAuditEventsTotal.WithLabelValues("gc_block_mapping_unresolved_ambiguous_representation"))

	resolved, err := store.ResolveBlockIDs(orgID, libID, "", []string{externalSHA1})
	if err != nil {
		t.Fatalf("ResolveBlockIDs: %v", err)
	}
	if len(resolved) != 1 || resolved[0] != externalSHA1 {
		t.Fatalf("resolved = %v, want original id [%s] left unresolved", resolved, externalSHA1)
	}

	afterAmbiguous := testutil.ToFloat64(metrics.GCAuditEventsTotal.WithLabelValues("gc_block_mapping_unresolved_ambiguous_representation"))
	if afterAmbiguous-beforeAmbiguous != 1 {
		t.Fatalf("ambiguous metric delta = %v, want 1", afterAmbiguous-beforeAmbiguous)
	}
}
