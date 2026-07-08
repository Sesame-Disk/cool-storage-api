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
// erroring or silently leaving the SHA-1 unresolved. This is the plain-only leg
// of the dual-probe.
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
// leg of the dual-probe: no live library row, no plaintext mapping, only the
// library's encrypted representation has a forward mapping. ResolveBlockIDs must
// still resolve it instead of only ever trying plain:v1.
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
// both have a forward mapping for the same external SHA-1, but they point to two
// DIFFERENT internal SHA-256 values (two unrelated libraries/orgs collided on the
// same client-visible hash). The resolver must refuse to guess — leave the id
// unresolved (never delete/rewrite the wrong reference) — and must record the
// ambiguity so it stays visible for drift/corruption alerting instead of a silent
// leak.
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
