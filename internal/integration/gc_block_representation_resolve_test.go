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
// hand-written to mirror CassandraStore's query shape, so these two pin the
// same contracts against the actual schema/driver instead of the in-memory
// map — in particular the deleted_libraries P1 fixed in 77339b79f, which the
// mock could not have caught (see block_representation.go / store_cassandra.go
// GetLibraryBlockRepresentationID doc comment for the full story).

// TestGC_GetLibraryBlockRepresentationID_NoLiveRow_RealCassandra verifies that
// a library with no live `libraries` row resolves to a clean gocql.ErrNotFound
// against real Cassandra, rather than the "Undefined column name
// block_representation_id in table deleted_libraries" InvalidRequest that
// existed before 77339b79f (deleted_libraries carries no representation
// column in this schema; that lands with gc_queue/DLQ durability in the
// follow-up PR).
func TestGC_GetLibraryBlockRepresentationID_NoLiveRow_RealCassandra(t *testing.T) {
	requireCassandra(t)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)

	orgID := uuid.New()
	libraryID := uuid.New() // deliberately never inserted into `libraries`

	// Soft-delete marker, exactly as write_helpers.go inserts it: no
	// representation column, because this schema doesn't have one yet.
	if err := database.Session().Query(
		`INSERT INTO deleted_libraries (library_id, org_id, deleted_at, storage_class) VALUES (?, ?, ?, ?)`,
		libraryID.String(), orgID.String(), time.Now(), "hot",
	).Exec(); err != nil {
		t.Fatalf("seed deleted_libraries: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Session().Query(`DELETE FROM deleted_libraries WHERE library_id = ?`, libraryID.String()).Exec()
	})

	// Sentinel comparison, not a message match: the pre-fix bug surfaced as a
	// driver InvalidRequest ("Undefined column name ...") which errors.Is would
	// also correctly reject, but asserting the sentinel directly keeps this test
	// from being coupled to either error's wording.
	if _, err := store.GetLibraryBlockRepresentationID(orgID, libraryID); !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("GetLibraryBlockRepresentationID error = %v, want gocql.ErrNotFound", err)
	}
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
