//go:build integration

package integration

import (
	"errors"
	"fmt"
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// Real-Cassandra counterpart to internal/gc/store_mock_test.go's
// GetLibraryBlockRepresentationID / ResolveBlockIDs coverage. The mock is
// hand-written to mirror CassandraStore's query shape, so these pin the same
// contracts against the actual schema/driver instead of the in-memory map.

// TestGC_GetLibraryBlockRepresentationID_DeletedLibrary_RealCassandra pins the
// Rama 3 durability contract against real Cassandra: with the live `libraries`
// row gone, GetLibraryBlockRepresentationID resolves the representation from the
// soft-deleted row's block_representation_id column (added by migration 010).
//   - a deleted row carrying a representation returns exactly that value;
//   - a deleted row with an empty representation fails closed with
//     gocql.ErrNotFound, so callers fall back to the conservative dual-probe
//     rather than resolving against a guess.
//
// This is the behavior that Rama 2 temporarily could not have — migration 009
// had no block_representation_id on deleted_libraries, so the reader had to skip
// that table entirely and always returned ErrNotFound here. Now that migration
// 010 adds the column, the reader consults it again; these cases prove it reads
// correctly and still fails closed on an absent value.
func TestGC_GetLibraryBlockRepresentationID_DeletedLibrary_RealCassandra(t *testing.T) {
	requireCassandra(t)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)

	t.Run("deleted row with representation resolves it", func(t *testing.T) {
		orgID := uuid.New()
		libraryID := uuid.New() // never inserted into live `libraries`
		representationID := dbpkg.EncryptedLibraryBlockRepresentationID(libraryID.String())

		if err := database.Session().Query(
			`INSERT INTO deleted_libraries (library_id, org_id, deleted_at, storage_class, block_representation_id) VALUES (?, ?, ?, ?, ?)`,
			libraryID.String(), orgID.String(), time.Now(), "hot", representationID,
		).Exec(); err != nil {
			t.Fatalf("seed deleted_libraries: %v", err)
		}
		t.Cleanup(func() {
			_ = database.Session().Query(`DELETE FROM deleted_libraries WHERE library_id = ?`, libraryID.String()).Exec()
		})

		got, err := store.GetLibraryBlockRepresentationID(orgID, libraryID)
		if err != nil {
			t.Fatalf("GetLibraryBlockRepresentationID: %v", err)
		}
		if got != representationID {
			t.Fatalf("representation = %q, want %q", got, representationID)
		}
	})

	t.Run("deleted row without representation fails closed", func(t *testing.T) {
		orgID := uuid.New()
		libraryID := uuid.New()

		// No block_representation_id column set (nil), e.g. a row written before
		// migration 010 backfill or a plaintext library whose value was empty.
		if err := database.Session().Query(
			`INSERT INTO deleted_libraries (library_id, org_id, deleted_at, storage_class) VALUES (?, ?, ?, ?)`,
			libraryID.String(), orgID.String(), time.Now(), "hot",
		).Exec(); err != nil {
			t.Fatalf("seed deleted_libraries: %v", err)
		}
		t.Cleanup(func() {
			_ = database.Session().Query(`DELETE FROM deleted_libraries WHERE library_id = ?`, libraryID.String()).Exec()
		})

		// Sentinel comparison, not a message match, so the test is not coupled to
		// the driver's error wording.
		if _, err := store.GetLibraryBlockRepresentationID(orgID, libraryID); !errors.Is(err, gocql.ErrNotFound) {
			t.Fatalf("GetLibraryBlockRepresentationID error = %v, want gocql.ErrNotFound", err)
		}
	})

	t.Run("org mismatch fails closed", func(t *testing.T) {
		orgID := uuid.New()
		otherOrgID := uuid.New()
		libraryID := uuid.New()
		representationID := dbpkg.EncryptedLibraryBlockRepresentationID(libraryID.String())

		if err := database.Session().Query(
			`INSERT INTO deleted_libraries (library_id, org_id, deleted_at, storage_class, block_representation_id) VALUES (?, ?, ?, ?, ?)`,
			libraryID.String(), orgID.String(), time.Now(), "hot", representationID,
		).Exec(); err != nil {
			t.Fatalf("seed deleted_libraries: %v", err)
		}
		t.Cleanup(func() {
			_ = database.Session().Query(`DELETE FROM deleted_libraries WHERE library_id = ?`, libraryID.String()).Exec()
		})

		if _, err := store.GetLibraryBlockRepresentationID(otherOrgID, libraryID); !errors.Is(err, gocql.ErrNotFound) {
			t.Fatalf("GetLibraryBlockRepresentationID error = %v, want gocql.ErrNotFound", err)
		}
	})
}

// TestGC_ResolveBlockIDs_AmbiguousDualProbe_RealCassandra is the DB-level
// integration counterpart to
// TestMockStore_ResolveBlockIDs_DualProbeAmbiguousLeavesUnresolvedAndCountsMetric:
// plaintext and encrypted representations within the same org map the same
// external SHA-1 to two different internal SHA-256 values, and the library has
// no live row (so GetLibraryBlockRepresentationID can't disambiguate). Against
// real Cassandra — not the mock's in-memory map — ResolveBlockIDs must still
// fail closed: leave the id unresolved rather than guess which internal is
// correct, and record the ambiguity metric so a silent leak stays visible.
func TestGC_ResolveBlockIDs_AmbiguousDualProbe_RealCassandra(t *testing.T) {
	requireCassandra(t)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)

	orgID := uuid.New()
	libraryID := uuid.New() // no live `libraries` row -> forces the dual-probe
	plainRep := dbpkg.PlainBlockRepresentationID
	encRep := dbpkg.EncryptedLibraryBlockRepresentationID(libraryID.String())

	content := []byte(fmt.Sprintf("ambiguous-dual-probe-%d", time.Now().UnixNano()))
	externalSHA1 := mcSHA1(content)
	internalPlain := mcSHA256(content)
	internalCipher := mcSHA256(append(content, []byte("-ciphertext")...))

	t.Cleanup(func() {
		_ = database.Session().Query(`DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`, orgID.String(), plainRep, externalSHA1).Exec()
		_ = database.Session().Query(`DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`, orgID.String(), encRep, externalSHA1).Exec()
	})

	if err := database.WriteVerifiedWebBlockMapping(orgID.String(), plainRep, externalSHA1, internalPlain, time.Now().UTC()); err != nil {
		t.Fatalf("seed plaintext mapping: %v", err)
	}
	if err := database.WriteBlockIDMapping(orgID.String(), encRep, externalSHA1, internalCipher, time.Now().UTC()); err != nil {
		t.Fatalf("seed encrypted mapping: %v", err)
	}

	beforeAmbiguous := testutil.ToFloat64(metrics.GCAuditEventsTotal.WithLabelValues("gc_block_mapping_unresolved_ambiguous_representation"))

	resolved, err := store.ResolveBlockIDs(orgID, libraryID, "", []string{externalSHA1})
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
