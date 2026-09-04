//go:build integration

package v2

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

var (
	restoreGuardDBOnce sync.Once
	restoreGuardDB     *dbpkg.DB
	restoreGuardDBErr  error
)

func restoreGuardDBForTest(t *testing.T) *dbpkg.DB {
	t.Helper()
	restoreGuardDBOnce.Do(func() {
		cfg := config.DatabaseConfig{
			Hosts:       restoreGuardHosts(restoreGuardEnv("CASSANDRA_HOSTS", "cassandra:9042")),
			Keyspace:    restoreGuardEnv("CASSANDRA_KEYSPACE", "sesamefs"),
			Consistency: restoreGuardEnv("CASSANDRA_CONSISTENCY", "LOCAL_QUORUM"),
			LocalDC:     restoreGuardEnv("CASSANDRA_LOCAL_DC", "datacenter1"),
			Username:    os.Getenv("CASSANDRA_USERNAME"),
			Password:    os.Getenv("CASSANDRA_PASSWORD"),
		}
		restoreGuardDB, restoreGuardDBErr = dbpkg.New(cfg)
	})
	if restoreGuardDBErr != nil {
		t.Fatalf("connect Cassandra for restore-guard test: %v", restoreGuardDBErr)
	}
	return restoreGuardDB
}

func restoreGuardEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func restoreGuardHosts(value string) []string {
	parts := strings.Split(value, ",")
	hosts := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			hosts = append(hosts, p)
		}
	}
	if len(hosts) == 0 {
		return []string{"cassandra:9042"}
	}
	return hosts
}

// A stale GC hard-delete lock can be stolen by restore's stale-aware lease
// acquisition. Because a Cassandra UPDATE is an upsert, restore must NOT be able
// to recreate the canonical `libraries` row over content a crashed worker already
// began purging. The only safe state to restore from is the original soft-deleted
// canonical row; if it is gone, restore must reject and leave `libraries` absent.
func TestRestoreDeletedLibrary_RejectsWhenCanonicalRowAbsent(t *testing.T) {
	db := restoreGuardDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()

	// Purge residue: a stale GC lock (heartbeat 2h old, so the lease is stealable),
	// but NO canonical `libraries` row.
	staleAt := time.Now().UTC().Add(-2 * time.Hour)
	if err := session.Query(`
		INSERT INTO gc_library_hard_delete_locks (library_id, started_at, heartbeat, lease_token)
		VALUES (?, ?, ?, ?)`,
		libraryID.String(), staleAt, staleAt, uuid.New().String()).Exec(); err != nil {
		t.Fatalf("seed stale lock: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM gc_library_hard_delete_locks WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
	})

	if err := restoreDeletedLibrary(db, orgID.String(), ownerID.String(), libraryID.String()); err == nil {
		t.Fatal("restore must reject when the canonical libraries row is absent")
	}

	var got string
	scanErr := session.Query(`SELECT library_id FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&got)
	if !errors.Is(scanErr, gocql.ErrNotFound) {
		t.Fatalf("canonical libraries row must remain absent after a rejected restore; scanErr=%v got=%q", scanErr, got)
	}
}

// A present-but-active canonical row (deleted_at == null) is not in trash: restore
// must reject rather than run its clearing batch.
func TestRestoreDeletedLibrary_RejectsWhenCanonicalRowActive(t *testing.T) {
	db := restoreGuardDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()

	if err := session.Query(`
		INSERT INTO libraries (org_id, library_id, owner_id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		orgID.String(), libraryID.String(), ownerID.String(), "restore-guard-active",
		time.Now().UTC(), time.Now().UTC()).Exec(); err != nil {
		t.Fatalf("seed active library: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
	})

	if err := restoreDeletedLibrary(db, orgID.String(), ownerID.String(), libraryID.String()); err == nil {
		t.Fatal("restore must reject a library that is not in trash (deleted_at is null)")
	}

	var deletedAt time.Time
	if err := session.Query(`SELECT deleted_at FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&deletedAt); err != nil {
		t.Fatalf("read canonical library after rejected restore: %v", err)
	}
	if !deletedAt.IsZero() {
		t.Fatal("active library must stay active (deleted_at null) after a rejected restore")
	}
}

// If the canonical ACTIVE transition committed but the marker/read-model batch
// did not, a retry must finish the restore instead of returning "not in trash"
// or applying aggregate counters a second time.
func TestRestoreDeletedLibrary_RetriesPendingFinalization(t *testing.T) {
	db := restoreGuardDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()
	createdAt := time.Now().UTC().Add(-4 * time.Hour).Truncate(time.Millisecond)
	updatedAt := createdAt.Add(time.Hour)
	deletedAt := createdAt.Add(2 * time.Hour)
	if err := session.Query(`
		INSERT INTO libraries (org_id, library_id, owner_id, name, encrypted, storage_class, size_bytes, file_count, created_at, updated_at, publication_state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		orgID.String(), libraryID.String(), ownerID.String(), "restore-pending-finalization", false, "hot", int64(0), int64(0), createdAt, updatedAt, dbpkg.LibraryPublicationStateActive).Exec(); err != nil {
		t.Fatalf("seed active library: %v", err)
	}
	if err := session.Query(`
		INSERT INTO deleted_libraries (library_id, org_id, deleted_at, storage_class)
		VALUES (?, ?, ?, ?)`, libraryID.String(), orgID.String(), deletedAt, "hot").Exec(); err != nil {
		t.Fatalf("seed pending restore marker: %v", err)
	}
	projectionRow := dbpkg.AdminLibraryProjectionRow{
		OrgID: orgID.String(), LibraryID: libraryID.String(), OwnerID: ownerID.String(),
		Name: "restore-pending-finalization", StorageClass: "hot", CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM gc_library_hard_delete_locks WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM deleted_libraries WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
		cleanup := session.Batch(gocql.LoggedBatch)
		dbpkg.AddDeleteAdminLibraryReadModelQuery(cleanup, projectionRow)
		cleanup.Query(`DELETE FROM gc_storage_counter_reconciliation WHERE scope = ?`, traffic.PlatformStorageScope())
		cleanup.Query(`DELETE FROM gc_storage_counter_reconciliation WHERE scope = ?`, traffic.OrganizationStorageScope(orgID.String()))
		cleanup.Query(`DELETE FROM gc_storage_counter_reconciliation WHERE scope = ?`, traffic.UserStorageScope(orgID.String(), ownerID.String()))
		_ = cleanup.Exec()
	})

	if err := restoreDeletedLibrary(db, orgID.String(), ownerID.String(), libraryID.String()); err != nil {
		t.Fatalf("restore retry should finalize the pending transition: %v", err)
	}
	var canonicalDeletedAt time.Time
	if err := session.Query(`SELECT deleted_at FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Scan(&canonicalDeletedAt); err != nil {
		t.Fatalf("read restored canonical library: %v", err)
	}
	if !canonicalDeletedAt.IsZero() {
		t.Fatalf("canonical library remains deleted after retry: %s", canonicalDeletedAt)
	}
	var markerID string
	markerErr := session.Query(`SELECT library_id FROM deleted_libraries WHERE library_id = ?`, libraryID.String()).Scan(&markerID)
	if !errors.Is(markerErr, gocql.ErrNotFound) {
		t.Fatalf("pending restore marker remains after retry: err=%v marker=%q", markerErr, markerID)
	}
}

// Rollback can run after the initial HEAD CAS succeeded (for example when a
// subsequent share write failed). It must revoke publication before deleting
// canonical rows and must leave the published commit for fenced orphan GC.
func TestRollbackNewLibrary_RevokesAuthorityAndRetainsPublishedCommit(t *testing.T) {
	db := restoreGuardDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()
	createdAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	updatedAt := createdAt.Add(time.Hour)
	commitID := uuid.New().String()
	if err := session.Query(`
		INSERT INTO libraries (org_id, library_id, owner_id, name, encrypted, storage_class, size_bytes, file_count, created_at, updated_at, head_commit_id, publication_state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		orgID.String(), libraryID.String(), ownerID.String(), "rollback-authority", false, "hot", int64(0), int64(0), createdAt, updatedAt, commitID, dbpkg.LibraryPublicationStateActive).Exec(); err != nil {
		t.Fatalf("seed published library: %v", err)
	}
	if err := session.Query(`
		INSERT INTO commits (library_id, commit_id, parent_id, root_fs_id, creator_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		libraryID.String(), commitID, "", "rollback-root", ownerID.String(), "rollback test commit", updatedAt).Exec(); err != nil {
		t.Fatalf("seed published commit: %v", err)
	}
	projectionRow := dbpkg.AdminLibraryProjectionRow{
		OrgID: orgID.String(), LibraryID: libraryID.String(), OwnerID: ownerID.String(),
		Name: "rollback-authority", StorageClass: "hot", CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM commits WHERE library_id = ? AND commit_id = ?`, libraryID.String(), commitID).Exec()
		_ = session.Query(`DELETE FROM library_publication_revocations WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries_by_id WHERE library_id = ?`, libraryID.String()).Exec()
		cleanup := session.Batch(gocql.LoggedBatch)
		dbpkg.AddDeleteAdminLibraryReadModelQuery(cleanup, projectionRow)
		_ = cleanup.Exec()
	})

	if err := rollbackNewLibrary(db, projectionRow); err != nil {
		t.Fatalf("rollbackNewLibrary: %v", err)
	}
	var gotCommit string
	if err := session.Query(`SELECT commit_id FROM commits WHERE library_id = ? AND commit_id = ?`, libraryID.String(), commitID).Scan(&gotCommit); err != nil {
		t.Fatalf("published commit must remain after rollback: %v", err)
	}
	var revokedID string
	if err := session.Query(`SELECT library_id FROM library_publication_revocations WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Consistency(gocql.EachQuorum).Scan(&revokedID); err != nil {
		t.Fatalf("rollback must leave durable publication revocation: %v", err)
	}
	var libraryRow string
	if err := session.Query(`SELECT library_id FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Scan(&libraryRow); !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("canonical library must be removed after rollback: err=%v row=%q", err, libraryRow)
	}
}

// Once hard-delete commits ACTIVE -> TERMINAL, restore must not clear deleted_at
// even while the canonical row remains visible before the physical row delete.
func TestRestoreDeletedLibrary_RejectsWhenPublicationAuthorityTerminal(t *testing.T) {
	db := restoreGuardDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()
	deletedAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := session.Query(`
		INSERT INTO libraries (org_id, library_id, owner_id, name, created_at, updated_at, deleted_at, publication_state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		orgID.String(), libraryID.String(), ownerID.String(), "restore-guard-terminal",
		now.Add(-4*time.Hour), now, deletedAt, dbpkg.LibraryPublicationStateActive).Exec(); err != nil {
		t.Fatalf("seed terminal-transition library: %v", err)
	}
	casState := map[string]interface{}{}
	applied, err := session.Query(`
		UPDATE libraries SET publication_state = ?
		WHERE org_id = ? AND library_id = ?
		IF publication_state = ?`,
		dbpkg.LibraryPublicationStateTerminal, orgID.String(), libraryID.String(), dbpkg.LibraryPublicationStateActive).
		SerialConsistency(gocql.Serial).MapScanCAS(casState)
	if err != nil {
		t.Fatalf("commit terminal publication authority: %v", err)
	}
	if !applied {
		t.Fatalf("expected ACTIVE -> TERMINAL transition, CAS state=%v", casState)
	}
	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM gc_library_hard_delete_locks WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM deleted_libraries WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
	})

	if err := restoreDeletedLibrary(db, orgID.String(), ownerID.String(), libraryID.String()); !errors.Is(err, dbpkg.ErrLibraryPublicationTerminal) {
		t.Fatalf("restore must reject terminal publication authority, got %v", err)
	}

	var canonicalDeletedAt time.Time
	var publicationState string
	if err := session.Query(`SELECT deleted_at, publication_state FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&canonicalDeletedAt, &publicationState); err != nil {
		t.Fatalf("read terminal library after rejected restore: %v", err)
	}
	if !canonicalDeletedAt.Equal(deletedAt) || publicationState != dbpkg.LibraryPublicationStateTerminal {
		t.Fatalf("terminal library changed after rejected restore: deleted_at=%s publication_state=%q", canonicalDeletedAt, publicationState)
	}
}

func TestRestoreDeletedLibrary_RejectsUnknownPublicationAuthority(t *testing.T) {
	db := restoreGuardDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()
	deletedAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := session.Query(`
		INSERT INTO libraries (org_id, library_id, owner_id, name, created_at, updated_at, deleted_at, publication_state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		orgID.String(), libraryID.String(), ownerID.String(), "restore-guard-unknown",
		now.Add(-4*time.Hour), now, deletedAt, "FUTURE_STATE").Exec(); err != nil {
		t.Fatalf("seed unknown-state library: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM gc_library_hard_delete_locks WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
	})

	if err := restoreDeletedLibrary(db, orgID.String(), ownerID.String(), libraryID.String()); err == nil {
		t.Fatal("restore must reject an unknown publication authority")
	}

	var canonicalDeletedAt time.Time
	var publicationState string
	if err := session.Query(`SELECT deleted_at, publication_state FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&canonicalDeletedAt, &publicationState); err != nil {
		t.Fatalf("read unknown-state library after rejected restore: %v", err)
	}
	if !canonicalDeletedAt.Equal(deletedAt) || publicationState != "FUTURE_STATE" {
		t.Fatalf("unknown-state library changed after rejected restore: deleted_at=%s publication_state=%q", canonicalDeletedAt, publicationState)
	}
}

// InitializeLibraryFS must not publish or reactivate a library whose terminal
// publication authority was already committed by hard-delete.
func TestInitializeLibraryFS_RejectsTerminalPublicationAuthority(t *testing.T) {
	db := restoreGuardDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := session.Query(`
		INSERT INTO libraries (org_id, library_id, owner_id, name, created_at, updated_at, publication_state)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		orgID.String(), libraryID.String(), ownerID.String(), "initialize-guard-terminal",
		now.Add(-4*time.Hour), now, dbpkg.LibraryPublicationStateTerminal).Exec(); err != nil {
		t.Fatalf("seed terminal library for initialization: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM fs_objects WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM commits WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries_by_id WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
	})

	err := NewFSHelper(db).InitializeLibraryFS(orgID.String(), libraryID.String(), ownerID.String(), "initialize-guard-terminal")
	if err == nil {
		t.Fatal("initialization must reject terminal publication authority")
	}

	var headCommitID string
	var publicationState string
	if err := session.Query(`SELECT head_commit_id, publication_state FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&headCommitID, &publicationState); err != nil {
		t.Fatalf("read terminal library after rejected initialization: %v", err)
	}
	if headCommitID != "" || publicationState != dbpkg.LibraryPublicationStateTerminal {
		t.Fatalf("terminal library changed after rejected initialization: head=%q publication_state=%q", headCommitID, publicationState)
	}
	// The commit must be cleaned up, but the content-addressed empty-root
	// fs_object must NOT be: it is identical for every initializer of this
	// library, so a concurrent winner could reference the exact row a rejected
	// cleanup would otherwise delete out from under it. See
	// deleteInitialLibraryFSArtifacts.
	var stagedCommitID string
	commitErr := session.Query(`SELECT commit_id FROM commits WHERE library_id = ? LIMIT 1`, libraryID.String()).Scan(&stagedCommitID)
	if !errors.Is(commitErr, gocql.ErrNotFound) {
		t.Fatalf("rejected initialization left a staged commit: commitErr=%v commitID=%q", commitErr, stagedCommitID)
	}
	var stagedFSID string
	if err := session.Query(`SELECT fs_id FROM fs_objects WHERE library_id = ? LIMIT 1`, libraryID.String()).Scan(&stagedFSID); err != nil {
		t.Fatalf("rejected initialization must preserve the shared content-addressed root fs_object: %v", err)
	}
}

// While a fresh permanent-delete lease is actively owned, restore must reject
// instead of clearing deleted_at and resurrecting a library whose hard delete is
// already in progress.
func TestRestoreDeletedLibrary_RejectsWhileHardDeleteLeaseOwned(t *testing.T) {
	db := restoreGuardDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()
	deletedAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	now := time.Now().UTC().Truncate(time.Millisecond)
	leaseToken := uuid.New()

	if err := session.Query(`
		INSERT INTO libraries (org_id, library_id, owner_id, name, created_at, updated_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		orgID.String(), libraryID.String(), ownerID.String(), "restore-guard-locked",
		now.Add(-4*time.Hour), now, deletedAt).Exec(); err != nil {
		t.Fatalf("seed trashed library: %v", err)
	}
	if err := session.Query(`
		INSERT INTO deleted_libraries (library_id, org_id, deleted_at, storage_class)
		VALUES (?, ?, ?, ?)`,
		libraryID.String(), orgID.String(), deletedAt, "hot").Exec(); err != nil {
		t.Fatalf("seed deleted_libraries marker: %v", err)
	}
	acquired, err := gcpkg.AcquireLibraryHardDeleteLockLease(session, libraryID, leaseToken)
	if err != nil {
		t.Fatalf("AcquireLibraryHardDeleteLockLease: %v", err)
	}
	if !acquired {
		t.Fatal("expected to acquire fresh library hard-delete lock")
	}
	t.Cleanup(func() {
		_ = gcpkg.ReleaseLibraryHardDeleteLockLease(session, libraryID, leaseToken)
		_ = session.Query(`DELETE FROM deleted_libraries WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
	})

	if err := restoreDeletedLibrary(db, orgID.String(), ownerID.String(), libraryID.String()); err == nil {
		t.Fatal("restore must reject while the library hard-delete lease is actively owned")
	}

	var canonicalDeletedAt time.Time
	if err := session.Query(`SELECT deleted_at FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&canonicalDeletedAt); err != nil {
		t.Fatalf("read trashed library after rejected restore: %v", err)
	}
	if !canonicalDeletedAt.Equal(deletedAt) {
		t.Fatalf("deleted_at = %s, want %s after rejected restore", canonicalDeletedAt, deletedAt)
	}
}

// Soft-delete, restore, and hard-delete must all serialize on the same library
// lifecycle fence. This is the dangerous shape from the audit: an ACTIVE
// canonical row with a durable delete marker. While another lifecycle writer
// owns the fence, none of the writers may consume or alter either row.
func TestLibraryLifecycleWritersRejectWhileFenceOwned(t *testing.T) {
	db := restoreGuardDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()
	deletedAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	now := time.Now().UTC().Truncate(time.Millisecond)
	leaseToken := uuid.New()

	if err := session.Query(`
		INSERT INTO libraries (org_id, library_id, owner_id, name, created_at, updated_at, publication_state)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		orgID.String(), libraryID.String(), ownerID.String(), "lifecycle-fence",
		now.Add(-4*time.Hour), now, dbpkg.LibraryPublicationStateActive).Exec(); err != nil {
		t.Fatalf("seed active library: %v", err)
	}
	if err := session.Query(`
		INSERT INTO deleted_libraries (library_id, org_id, deleted_at, storage_class)
		VALUES (?, ?, ?, ?)`,
		libraryID.String(), orgID.String(), deletedAt, "hot").Exec(); err != nil {
		t.Fatalf("seed deleted_libraries marker: %v", err)
	}
	acquired, err := gcpkg.AcquireLibraryHardDeleteLockLease(session, libraryID, leaseToken)
	if err != nil {
		t.Fatalf("AcquireLibraryHardDeleteLockLease: %v", err)
	}
	if !acquired {
		t.Fatal("expected to acquire fresh library lifecycle fence")
	}
	t.Cleanup(func() {
		_ = gcpkg.ReleaseLibraryHardDeleteLockLease(session, libraryID, leaseToken)
		_ = session.Query(`DELETE FROM deleted_libraries WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
	})

	if err := softDeleteLibrary(db, orgID.String(), ownerID.String(), ownerID.String(), libraryID.String()); err == nil {
		t.Fatal("API soft-delete must reject while the library lifecycle fence is owned")
	}
	if err := gcpkg.NewCassandraStore(db).SoftDeleteLibrary(orgID, libraryID, ownerID); err == nil {
		t.Fatal("GC soft-delete must reject while the library lifecycle fence is owned")
	}
	if err := restoreDeletedLibrary(db, orgID.String(), ownerID.String(), libraryID.String()); err == nil {
		t.Fatal("restore must reject while the library lifecycle fence is owned")
	}

	var canonicalDeletedAt time.Time
	if err := session.Query(`SELECT deleted_at FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&canonicalDeletedAt); err != nil {
		t.Fatalf("read canonical library after rejected lifecycle writers: %v", err)
	}
	if !canonicalDeletedAt.IsZero() {
		t.Fatalf("active canonical row changed while lifecycle fence was owned: deleted_at=%s", canonicalDeletedAt)
	}
	var markerID string
	if err := session.Query(`SELECT library_id FROM deleted_libraries WHERE library_id = ?`, libraryID.String()).Scan(&markerID); err != nil {
		t.Fatalf("delete marker disappeared while lifecycle fence was owned: %v", err)
	}
}
