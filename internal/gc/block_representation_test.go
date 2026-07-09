package gc

import (
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/google/uuid"
)

func TestResolveRequiredLibraryBlockRepresentation_UsesProvidedRepresentation(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	libraryID := uuid.New()
	provided := db.EncryptedLibraryBlockRepresentationID(libraryID.String())

	got, err := resolveRequiredLibraryBlockRepresentation(store, orgID, libraryID, provided, "provided test")
	if err != nil {
		t.Fatalf("resolveRequiredLibraryBlockRepresentation() error = %v, want nil", err)
	}
	if got != provided {
		t.Fatalf("resolveRequiredLibraryBlockRepresentation() = %q, want %q", got, provided)
	}
}

func TestResolveRequiredLibraryBlockRepresentation_ResolvesFromLiveLibrary(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	libraryID := uuid.New()
	store.AddLibrary(orgID, libraryID, "hot")

	got, err := resolveRequiredLibraryBlockRepresentation(store, orgID, libraryID, "", "resolve test")
	if err != nil {
		t.Fatalf("resolveRequiredLibraryBlockRepresentation() error = %v, want nil", err)
	}
	if got != db.PlainBlockRepresentationID {
		t.Fatalf("resolveRequiredLibraryBlockRepresentation() = %q, want %q", got, db.PlainBlockRepresentationID)
	}
}

func TestResolveRequiredLibraryBlockRepresentation_MissingMetadataIsFatal(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	libraryID := uuid.New()

	// Deleted library whose persisted representation was never recorded and whose
	// canonical row is gone: unresolvable, so enqueue must fail rather than guess.
	store.AddDeletedLibrary(orgID, libraryID, "hot", time.Now().UTC())
	store.mu.Lock()
	delete(store.libraries, libraryID)
	store.deletedLibraries[libraryID].BlockRepresentationID = ""
	store.mu.Unlock()

	got, err := resolveRequiredLibraryBlockRepresentation(store, orgID, libraryID, "", "strict test")
	if err == nil {
		t.Fatal("resolveRequiredLibraryBlockRepresentation() error = nil, want non-nil")
	}
	if got != "" {
		t.Fatalf("resolveRequiredLibraryBlockRepresentation() = %q, want empty result on error", got)
	}
}

func TestResolveRequiredLibraryBlockRepresentation_NonCanonicalIsFatal(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	libraryID := uuid.New()

	got, err := resolveRequiredLibraryBlockRepresentation(store, orgID, libraryID, "foo", "canonical test")
	if err == nil {
		t.Fatal("resolveRequiredLibraryBlockRepresentation() error = nil, want non-canonical error")
	}
	if got != "" {
		t.Fatalf("resolveRequiredLibraryBlockRepresentation() = %q, want empty result on error", got)
	}
	if !strings.Contains(err.Error(), "non-canonical") {
		t.Fatalf("error = %v, want non-canonical message", err)
	}
}

func TestResolveRequiredLibraryBlockRepresentation_WrongLibraryIsFatal(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	libraryID := uuid.New()
	otherLibraryID := uuid.New()

	got, err := resolveRequiredLibraryBlockRepresentation(store, orgID, libraryID, db.EncryptedLibraryBlockRepresentationID(otherLibraryID.String()), "library match test")
	if err == nil {
		t.Fatal("resolveRequiredLibraryBlockRepresentation() error = nil, want different-library error")
	}
	if got != "" {
		t.Fatalf("resolveRequiredLibraryBlockRepresentation() = %q, want empty result on error", got)
	}
	if !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("error = %v, want different-library message", err)
	}
}
