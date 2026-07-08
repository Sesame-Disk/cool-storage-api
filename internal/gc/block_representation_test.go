package gc

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestResolveRequiredLibraryBlockRepresentation_AllowsLegacyMissingMetadata(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	libraryID := uuid.New()

	store.AddDeletedLibrary(orgID, libraryID, "hot", time.Now().UTC())
	store.mu.Lock()
	delete(store.libraries, libraryID)
	store.deletedLibraries[libraryID].BlockRepresentationID = ""
	store.mu.Unlock()

	got, err := resolveRequiredLibraryBlockRepresentation(store, orgID, libraryID, "", "legacy test", true)
	if err != nil {
		t.Fatalf("resolveRequiredLibraryBlockRepresentation() error = %v, want nil", err)
	}
	if got != "" {
		t.Fatalf("resolveRequiredLibraryBlockRepresentation() = %q, want empty fallback", got)
	}
}

func TestResolveRequiredLibraryBlockRepresentation_RequiresMetadataWithoutLegacyFallback(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	libraryID := uuid.New()

	store.AddDeletedLibrary(orgID, libraryID, "hot", time.Now().UTC())
	store.mu.Lock()
	delete(store.libraries, libraryID)
	store.deletedLibraries[libraryID].BlockRepresentationID = ""
	store.mu.Unlock()

	got, err := resolveRequiredLibraryBlockRepresentation(store, orgID, libraryID, "", "strict test", false)
	if err == nil {
		t.Fatal("resolveRequiredLibraryBlockRepresentation() error = nil, want non-nil")
	}
	if got != "" {
		t.Fatalf("resolveRequiredLibraryBlockRepresentation() = %q, want empty result on error", got)
	}
}