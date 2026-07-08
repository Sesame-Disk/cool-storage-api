package gc

import (
	"errors"
	"testing"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

func TestMockStore_GetLibraryBlockRepresentationID_RejectsOrgMismatchOnLiveLibrary(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	otherOrgID := uuid.New()
	libID := uuid.New()

	store.AddDeletedLibrary(orgID, libID, "hot", time.Now().UTC())

	if _, err := store.GetLibraryBlockRepresentationID(otherOrgID, libID); !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("GetLibraryBlockRepresentationID error = %v, want %v", err, gocql.ErrNotFound)
	}
}

// TestMockStore_GetLibraryBlockRepresentationID_NoLiveRowIgnoresDeletedLibraries
// pins the fix for the schema gap where deleted_libraries has no
// block_representation_id column yet: a library absent from the live libraries
// table must resolve to gocql.ErrNotFound even when deletedLibraries holds a
// row for it, never a value read off the deleted-library marker.
func TestMockStore_GetLibraryBlockRepresentationID_NoLiveRowIgnoresDeletedLibraries(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
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
}

// TestMockStore_ResolveBlockIDs_NoLiveLibraryUsesDualProbeFallback verifies that
// once a library has no live row (the case after hard-delete, or before Rama 3
// gives gc_queue items a persisted representation), ResolveBlockIDs falls back
// to probing both the plaintext and encrypted representations instead of
// erroring or silently leaving the SHA-1 unresolved.
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
