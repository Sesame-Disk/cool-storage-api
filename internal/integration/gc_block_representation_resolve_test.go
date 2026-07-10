//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
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

// A missing canonical library is already-completed work during a retrying
// user/org cascade. SoftDeleteLibrary must therefore succeed without creating a
// deleted_libraries marker that could later trigger a phantom hard-delete.
func TestGC_SoftDeleteLibrary_MissingLibraryIsIdempotent_RealCassandra(t *testing.T) {
	requireCassandra(t)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)

	orgID := uuid.New()
	libraryID := uuid.New()
	t.Cleanup(func() {
		_ = database.Session().Query(`DELETE FROM deleted_libraries WHERE library_id = ?`, libraryID.String()).Exec()
	})

	if err := store.SoftDeleteLibrary(orgID, libraryID, uuid.New()); err != nil {
		t.Fatalf("SoftDeleteLibrary on missing library: %v", err)
	}

	var markerLibraryID string
	err := database.Session().Query(
		`SELECT library_id FROM deleted_libraries WHERE library_id = ?`, libraryID.String(),
	).Scan(&markerLibraryID)
	if !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("deleted_libraries marker unexpectedly exists: library_id=%q err=%v", markerLibraryID, err)
	}
}

// Policy discovery is only a projection. The Cassandra store must re-read the
// canonical row and surface representation drift as per-row metadata so the
// scanner can skip that library without aborting unrelated policy processing.
func TestGC_ListLibrariesByPolicy_InvalidRepresentationIsPerRowDrift_RealCassandra(t *testing.T) {
	requireCassandra(t)
	database := shareProjectionDBForTest(t)
	session := database.Session()
	store := gcpkg.NewCassandraStore(database)

	orgID := uuid.New()
	libraryID := uuid.New()
	headCommitID := fmt.Sprintf("policy-head-%d", time.Now().UnixNano())
	bucket := dbpkg.GCDiscoveryBucket(libraryID.String())
	now := time.Now().UTC()

	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM gc_libraries_by_policy WHERE policy_type = ? AND bucket = ? AND org_id = ? AND library_id = ?`,
			dbpkg.GCLibraryPolicyVersionTTL, bucket, orgID.String(), libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
	})

	// An encrypted library stamped plain:v1 is canonical in shape but belongs to
	// the wrong mapping domain and must never be queued for GC.
	if err := session.Query(`
		INSERT INTO libraries (
			org_id, library_id, encrypted, block_representation_id,
			head_commit_id, version_ttl_days, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, orgID.String(), libraryID.String(), true, dbpkg.PlainBlockRepresentationID,
		headCommitID, 30, now, now).Exec(); err != nil {
		t.Fatalf("seed libraries policy row: %v", err)
	}
	if err := session.Query(`
		INSERT INTO gc_libraries_by_policy (
			policy_type, bucket, org_id, library_id, days, cached_head_commit_id, policy_updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, dbpkg.GCLibraryPolicyVersionTTL, bucket, orgID.String(), libraryID.String(), 30, headCommitID, now).Exec(); err != nil {
		t.Fatalf("seed gc_libraries_by_policy: %v", err)
	}

	rows, err := store.ListLibrariesWithVersionTTL()
	if err != nil {
		t.Fatalf("ListLibrariesWithVersionTTL: %v", err)
	}
	for _, row := range rows {
		if row.OrgID != orgID || row.LibraryID != libraryID {
			continue
		}
		if !row.RepresentationInvalid {
			t.Fatalf("RepresentationInvalid = false, want true: %+v", row)
		}
		if row.BlockRepresentationID != dbpkg.PlainBlockRepresentationID {
			t.Fatalf("raw representation = %q, want %q", row.BlockRepresentationID, dbpkg.PlainBlockRepresentationID)
		}
		return
	}
	t.Fatalf("seeded policy row %s/%s was not returned", orgID, libraryID)
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

// TestGC_DeletedLibraryRepresentation_CascadeLifecycle_RealCassandra pins the
// deleted-library scanner-to-cascade-to-child-queue durability path against
// real Cassandra:
//
//  1. scanner sees an expired deleted_libraries row whose
//     block_representation_id was persisted at soft-delete time;
//  2. it enqueues a library_cascade row carrying that exact representation; then
//  3. the worker processes the cascade, hard-deletes the library rows, and
//     enqueues commit/fs_object children that still carry the same persisted
//     representation.
//
// This is the real DB counterpart to the unit coverage around
// scanExpiredDeletedLibraries + processLibraryCascade, and it closes the gap
// where the resolver/dual-probe tests alone did not prove the queue hand-off
// from deleted_libraries to durable child work.
func TestGC_DeletedLibraryRepresentation_CascadeLifecycle_RealCassandra(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	session := database.Session()
	store := gcpkg.NewCassandraStore(database)
	queue := gcpkg.NewQueue(store)
	scanner := gcpkg.NewScanner(store, queue, &gcpkg.Stats{}, config.GCConfig{TrashRetentionDays: 30})
	worker := gcpkg.NewWorker(store, nil, queue, 100, 0, false, &gcpkg.Stats{})

	orgID := uuid.New()
	libraryID := uuid.New()
	ownerID := uuid.New()
	commitID := fmt.Sprintf("commit-cascade-%d", time.Now().UnixNano())
	rootFSID := fmt.Sprintf("fs-root-%d", time.Now().UnixNano())
	representationID := dbpkg.EncryptedLibraryBlockRepresentationID(libraryID.String())
	deletedAt := time.Now().AddDate(0, 0, -45).UTC().Truncate(time.Millisecond)
	now := time.Now().UTC().Truncate(time.Millisecond)

	t.Cleanup(func() {
		_ = store.RemoveOrgFromActiveSet(orgID, time.Now().UTC().Add(time.Hour))
		_ = session.Query(`DELETE FROM gc_queue WHERE org_id = ? AND bucket = ? AND queued_at = ? AND item_type = ? AND item_id = ?`,
			orgID.String(), gcpkg.QueueBucket(orgID, gcpkg.ItemLibraryCascade, libraryID.String()), deletedAt, string(gcpkg.ItemLibraryCascade), libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM gc_queue WHERE org_id = ? AND bucket = ? AND queued_at = ? AND item_type = ? AND item_id = ?`,
			orgID.String(), gcpkg.QueueBucket(orgID, gcpkg.ItemCommit, commitID), deletedAt, string(gcpkg.ItemCommit), commitID).Exec()
		_ = session.Query(`DELETE FROM gc_queue WHERE org_id = ? AND bucket = ? AND queued_at = ? AND item_type = ? AND item_id = ?`,
			orgID.String(), gcpkg.QueueBucket(orgID, gcpkg.ItemFSObject, rootFSID), deletedAt, string(gcpkg.ItemFSObject), rootFSID).Exec()
		_ = session.Query(`DELETE FROM gc_pending_items WHERE org_id = ? AND bucket = ? AND item_type = ? AND library_id = ? AND item_id = ? AND identity_at = ?`,
			orgID.String(), gcpkg.PendingItemBucket(orgID, uuid.Nil, gcpkg.ItemLibraryCascade, libraryID.String()), string(gcpkg.ItemLibraryCascade), uuid.Nil.String(), libraryID.String(), deletedAt).Exec()
		_ = session.Query(`DELETE FROM gc_pending_items WHERE org_id = ? AND bucket = ? AND item_type = ? AND library_id = ? AND item_id = ? AND identity_at = ?`,
			orgID.String(), gcpkg.PendingItemBucket(orgID, libraryID, gcpkg.ItemCommit, commitID), string(gcpkg.ItemCommit), libraryID.String(), commitID, deletedAt).Exec()
		_ = session.Query(`DELETE FROM gc_pending_items WHERE org_id = ? AND bucket = ? AND item_type = ? AND library_id = ? AND item_id = ? AND identity_at = ?`,
			orgID.String(), gcpkg.PendingItemBucket(orgID, libraryID, gcpkg.ItemFSObject, rootFSID), string(gcpkg.ItemFSObject), libraryID.String(), rootFSID, deletedAt).Exec()
		_ = session.Query(`DELETE FROM commits WHERE library_id = ? AND commit_id = ?`, libraryID.String(), commitID).Exec()
		_ = session.Query(`DELETE FROM fs_objects WHERE library_id = ? AND fs_id = ?`, libraryID.String(), rootFSID).Exec()
		_ = session.Query(`DELETE FROM deleted_libraries WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries_by_id WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
	})

	if err := session.Query(`
		INSERT INTO libraries (
			org_id, library_id, owner_id, name, description, encrypted, enc_version,
			root_commit_id, head_commit_id, storage_class, size_bytes, file_count,
			deleted_at, deleted_by, created_at, updated_at, block_representation_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		orgID.String(), libraryID.String(), ownerID.String(), "gc-cascade-test", "",
		true, 2, commitID, commitID, "hot", int64(0), int64(1), deletedAt,
		ownerID.String(), now, now, representationID,
	).Exec(); err != nil {
		t.Fatalf("seed libraries: %v", err)
	}
	if err := session.Query(`
		INSERT INTO libraries_by_id (
			library_id, org_id, owner_id, name, head_commit_id, encrypted, enc_version,
			block_representation_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		libraryID.String(), orgID.String(), ownerID.String(), "gc-cascade-test", commitID,
		true, 2, representationID,
	).Exec(); err != nil {
		t.Fatalf("seed libraries_by_id: %v", err)
	}
	if err := session.Query(`
		INSERT INTO deleted_libraries (library_id, org_id, deleted_at, storage_class, block_representation_id)
		VALUES (?, ?, ?, ?, ?)
	`, libraryID.String(), orgID.String(), deletedAt, "hot", representationID).Exec(); err != nil {
		t.Fatalf("seed deleted_libraries: %v", err)
	}
	if err := session.Query(`
		INSERT INTO commits (library_id, commit_id, parent_id, root_fs_id, creator_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, libraryID.String(), commitID, "", rootFSID, ownerID.String(), "gc representation cascade", now).Exec(); err != nil {
		t.Fatalf("seed commits: %v", err)
	}
	if err := session.Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, full_path, size_bytes, mtime, block_ids)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, libraryID.String(), rootFSID, "file", rootFSID, "/", int64(0), now.Unix(), []string{}).Exec(); err != nil {
		t.Fatalf("seed fs_objects: %v", err)
	}

	if n, err := scanner.ScanExpiredDeletedLibrariesOnce(context.Background()); err != nil {
		t.Fatalf("ScanExpiredDeletedLibrariesOnce: %v", err)
	} else if n != 1 {
		t.Fatalf("ScanExpiredDeletedLibrariesOnce enqueued %d items, want 1", n)
	}
	// Keep background GC workers from stealing this org's queued cascade while
	// the test inspects the hand-off and drives the worker explicitly below.
	if err := store.RemoveOrgFromActiveSet(orgID, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("RemoveOrgFromActiveSet(after scan): %v", err)
	}

	items, err := store.DequeueBatch(orgID, 10, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("DequeueBatch(after scanner): %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 queued library cascade after scanner, got %d (%+v)", len(items), items)
	}
	if items[0].ItemType != gcpkg.ItemLibraryCascade || items[0].ItemID != libraryID.String() {
		t.Fatalf("scanner queued unexpected item: %+v", items[0])
	}
	if items[0].BlockRepresentationID != representationID {
		t.Fatalf("library_cascade BlockRepresentationID = %q, want %q", items[0].BlockRepresentationID, representationID)
	}

	processed, err := worker.ProcessOrgOnce(context.Background(), orgID)
	if err != nil {
		t.Fatalf("ProcessOrgOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("ProcessOrgOnce processed %d items, want 1 library cascade", processed)
	}

	if deletedMarker, err := store.GetLibraryDeletedAt(libraryID); err != nil {
		t.Fatalf("GetLibraryDeletedAt(after worker): %v", err)
	} else if deletedMarker != nil {
		t.Fatalf("expected deleted_libraries row to be hard-deleted, still found deleted_at=%v", *deletedMarker)
	}
	var liveLibraryID string
	if err := session.Query(`SELECT library_id FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Scan(&liveLibraryID); !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("libraries row still present or query failed after worker: library_id=%q err=%v", liveLibraryID, err)
	}
	var projectedLibraryID string
	if err := session.Query(`SELECT library_id FROM libraries_by_id WHERE library_id = ?`, libraryID.String()).Scan(&projectedLibraryID); !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("libraries_by_id row still present or query failed after worker: library_id=%q err=%v", projectedLibraryID, err)
	}

	children, err := store.DequeueBatch(orgID, 10, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("DequeueBatch(after worker): %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("expected 2 child items after library cascade, got %d (%+v)", len(children), children)
	}

	gotByType := make(map[gcpkg.ItemType]gcpkg.QueueItem, len(children))
	for _, item := range children {
		gotByType[item.ItemType] = item
		if item.BlockRepresentationID != representationID {
			t.Fatalf("child %s/%s BlockRepresentationID = %q, want %q", item.ItemType, item.ItemID, item.BlockRepresentationID, representationID)
		}
		if !item.IdentityAt.Equal(deletedAt) {
			t.Fatalf("child %s/%s IdentityAt = %v, want %v", item.ItemType, item.ItemID, item.IdentityAt, deletedAt)
		}
		if !item.RequiresLibraryDeletedCheck {
			t.Fatalf("child %s/%s RequiresLibraryDeletedCheck = false, want true", item.ItemType, item.ItemID)
		}
	}

	if commitItem, ok := gotByType[gcpkg.ItemCommit]; !ok || commitItem.ItemID != commitID {
		t.Fatalf("missing commit child %s in %+v", commitID, children)
	}
	if fsItem, ok := gotByType[gcpkg.ItemFSObject]; !ok || fsItem.ItemID != rootFSID {
		t.Fatalf("missing fs_object child %s in %+v", rootFSID, children)
	}
}
