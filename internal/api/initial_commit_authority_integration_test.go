//go:build integration

package api

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

var (
	initialCommitAuthorityDBOnce sync.Once
	initialCommitAuthorityDB     *dbpkg.DB
	initialCommitAuthorityDBErr  error
)

func initialCommitAuthorityDBForTest(t *testing.T) *dbpkg.DB {
	t.Helper()
	initialCommitAuthorityDBOnce.Do(func() {
		hosts := strings.Split(initialCommitAuthorityEnv("CASSANDRA_HOSTS", "cassandra:9042"), ",")
		cleanHosts := make([]string, 0, len(hosts))
		for _, host := range hosts {
			if host = strings.TrimSpace(host); host != "" {
				cleanHosts = append(cleanHosts, host)
			}
		}
		if len(cleanHosts) == 0 {
			cleanHosts = []string{"cassandra:9042"}
		}
		initialCommitAuthorityDB, initialCommitAuthorityDBErr = dbpkg.New(config.DatabaseConfig{
			Hosts:       cleanHosts,
			Keyspace:    initialCommitAuthorityEnv("CASSANDRA_KEYSPACE", "sesamefs"),
			Consistency: initialCommitAuthorityEnv("CASSANDRA_CONSISTENCY", "LOCAL_QUORUM"),
			LocalDC:     initialCommitAuthorityEnv("CASSANDRA_LOCAL_DC", "datacenter1"),
			Username:    os.Getenv("CASSANDRA_USERNAME"),
			Password:    os.Getenv("CASSANDRA_PASSWORD"),
		})
	})
	if initialCommitAuthorityDBErr != nil {
		t.Fatalf("connect Cassandra for initial-commit authority test: %v", initialCommitAuthorityDBErr)
	}
	return initialCommitAuthorityDB
}

func initialCommitAuthorityEnv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func TestCreateInitialCommit_RejectsTerminalPublicationAuthority(t *testing.T) {
	db := initialCommitAuthorityDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := session.Query(`
		INSERT INTO libraries (org_id, library_id, owner_id, name, created_at, updated_at, publication_state)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		orgID.String(), libraryID.String(), ownerID.String(), "initial-commit-guard-terminal",
		now.Add(-4*time.Hour), now, dbpkg.LibraryPublicationStateTerminal).Exec(); err != nil {
		t.Fatalf("seed terminal library for initial commit: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM fs_objects WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM commits WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries_by_id WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
	})

	_, err := (&SyncHandler{db: db}).createInitialCommit(libraryID.String(), orgID.String(), ownerID.String())
	if err == nil {
		t.Fatal("initial commit must reject terminal publication authority")
	}

	var headCommitID string
	var publicationState string
	if err := session.Query(`SELECT head_commit_id, publication_state FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&headCommitID, &publicationState); err != nil {
		t.Fatalf("read terminal library after rejected initial commit: %v", err)
	}
	if headCommitID != "" || publicationState != dbpkg.LibraryPublicationStateTerminal {
		t.Fatalf("terminal library changed after rejected initial commit: head=%q publication_state=%q", headCommitID, publicationState)
	}
	// The commit must be cleaned up, but the content-addressed empty-root
	// fs_object must NOT be: it is identical for every initializer of this
	// library, so a concurrent winner could reference the exact row a rejected
	// cleanup would otherwise delete out from under it. See
	// deleteInitialCommitArtifacts.
	var stagedCommitID string
	commitErr := session.Query(`SELECT commit_id FROM commits WHERE library_id = ? LIMIT 1`, libraryID.String()).Scan(&stagedCommitID)
	if !errors.Is(commitErr, gocql.ErrNotFound) {
		t.Fatalf("rejected initial commit left a staged commit: commitErr=%v commitID=%q", commitErr, stagedCommitID)
	}
	var stagedFSID string
	if err := session.Query(`SELECT fs_id FROM fs_objects WHERE library_id = ? LIMIT 1`, libraryID.String()).Scan(&stagedFSID); err != nil {
		t.Fatalf("rejected initial commit must preserve the shared content-addressed root fs_object: %v", err)
	}
}

// TestCreateInitialCommit_LoserCleanupPreservesWinnersSharedRoot reproduces
// the initializer race two independent auditors flagged as the top blocker:
// the empty-root fs_object is content-addressed ("1\n[]" hashed), so every
// initializer of the same library computes the identical fs_id. A second
// initializer call deterministically loses the HEAD CAS once the first has
// published (no goroutine timing needed to force the race), and its cleanup
// must delete only its own rejected commit -- never the shared root the
// winner's already-published commit depends on.
func TestCreateInitialCommit_LoserCleanupPreservesWinnersSharedRoot(t *testing.T) {
	db := initialCommitAuthorityDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := session.Query(`
		INSERT INTO libraries (org_id, library_id, owner_id, name, created_at, updated_at, publication_state)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		orgID.String(), libraryID.String(), ownerID.String(), "initial-commit-shared-root",
		now, now, dbpkg.LibraryPublicationStateActive).Exec(); err != nil {
		t.Fatalf("seed active library: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM fs_objects WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM commits WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries_by_id WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
	})

	handler := &SyncHandler{db: db}

	winnerCommitID, err := handler.createInitialCommit(libraryID.String(), orgID.String(), ownerID.String())
	if err != nil {
		t.Fatalf("first (winning) initializer must succeed: %v", err)
	}
	if _, err := handler.createInitialCommit(libraryID.String(), orgID.String(), ownerID.String()); err == nil {
		t.Fatal("second (losing) initializer must be rejected: HEAD is already published")
	}

	var headCommitID string
	if err := session.Query(`SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&headCommitID); err != nil {
		t.Fatalf("read published head: %v", err)
	}
	if headCommitID != winnerCommitID {
		t.Fatalf("head_commit_id = %q, want winner's commit %q", headCommitID, winnerCommitID)
	}

	var winnerRootFSID string
	if err := session.Query(`SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?`,
		libraryID.String(), winnerCommitID).Scan(&winnerRootFSID); err != nil {
		t.Fatalf("winner's commit must survive the loser's cleanup: %v", err)
	}
	var storedRootFSID string
	if err := session.Query(`SELECT fs_id FROM fs_objects WHERE library_id = ? AND fs_id = ?`,
		libraryID.String(), winnerRootFSID).Scan(&storedRootFSID); err != nil {
		t.Fatalf("winner's shared root fs_object must survive the loser's cleanup: %v", err)
	}

	iter := session.Query(`SELECT commit_id FROM commits WHERE library_id = ?`, libraryID.String()).Iter()
	var commitIDs []string
	var commitID string
	for iter.Scan(&commitID) {
		commitIDs = append(commitIDs, commitID)
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("list commits: %v", err)
	}
	if len(commitIDs) != 1 || commitIDs[0] != winnerCommitID {
		t.Fatalf("commits for library = %v, want exactly [%q]", commitIDs, winnerCommitID)
	}
}

// TestCreateInitialCommit_RejectsResurrectionOfRevokedAbsentLibrary covers the
// second blocker: a NULL publication_state in a rejected CAS is ambiguous
// between "genuine untouched pre-021 row" and "absent partition", and Cassandra
// itself cannot distinguish them. A library whose canonical row was already
// hard-deleted (row absent, durable revocation witness present) must not be
// resurrected as ACTIVE by a stale initializer racing behind the delete.
func TestCreateInitialCommit_RejectsResurrectionOfRevokedAbsentLibrary(t *testing.T) {
	db := initialCommitAuthorityDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()

	// Simulate the post-hard-delete state directly: canonical row absent,
	// durable revocation witness present. Seeding this via
	// dbpkg.RevokeLibraryPublication would not reproduce it: that helper's own
	// legacy-NULL retry upserts a bare TERMINAL tombstone row for a library ID
	// that does not otherwise exist, which is a different (already-covered)
	// state -- see TestCreateInitialCommit_RejectsTerminalPublicationAuthority.
	if err := session.Query(`
		INSERT INTO library_publication_revocations (org_id, library_id, revoked_at)
		VALUES (?, ?, ?)`, orgID.String(), libraryID.String(), time.Now().UTC()).
		Consistency(gocql.EachQuorum).Exec(); err != nil {
		t.Fatalf("seed revocation witness for absent library: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM fs_objects WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM commits WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM library_publication_revocations WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
	})

	if _, err := (&SyncHandler{db: db}).createInitialCommit(libraryID.String(), orgID.String(), ownerID.String()); err == nil {
		t.Fatal("initial commit must not resurrect a revoked, hard-deleted library")
	}

	var storedLibraryID string
	err := session.Query(`SELECT library_id FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&storedLibraryID)
	if !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("canonical library row must remain absent after a rejected resurrection attempt: err=%v row=%q", err, storedLibraryID)
	}
	if revoked, err := dbpkg.IsLibraryPublicationRevoked(session, orgID.String(), libraryID.String()); err != nil {
		t.Fatalf("read revocation witness: %v", err)
	} else if !revoked {
		t.Fatal("revocation witness must remain after a rejected resurrection attempt")
	}
	var stagedCommitID string
	commitErr := session.Query(`SELECT commit_id FROM commits WHERE library_id = ? LIMIT 1`, libraryID.String()).Scan(&stagedCommitID)
	if !errors.Is(commitErr, gocql.ErrNotFound) {
		t.Fatalf("rejected resurrection attempt left a staged commit: commitErr=%v commitID=%q", commitErr, stagedCommitID)
	}
}

// TestCreateInitialCommit_RejectsRetryOnGenuinelyAbsentLibraryRow closes the
// TOCTOU gap in the resurrection guard: IsLibraryPublicationRevoked and the
// legacy retry CAS are two separate round trips, not one atomic operation, so
// a hard-delete that revokes and removes the canonical row strictly between
// them would observe revoked=false yet still face an absent row at retry
// time -- the witness check alone cannot close that window because it lives
// in a different table. This test reproduces the retry CAS's observable state
// in that window directly: no canonical row and no witness at all (as if the
// witness check ran and returned false a moment before a concurrent
// hard-delete both wrote the witness and removed the row). Before the
// owner_id-guarded retry CAS, Cassandra's IF clause read every column of the
// absent partition as NULL and materialized a phantom ACTIVE library that was
// never created through any normal path.
func TestCreateInitialCommit_RejectsRetryOnGenuinelyAbsentLibraryRow(t *testing.T) {
	db := initialCommitAuthorityDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()

	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM fs_objects WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM commits WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
	})

	if _, err := (&SyncHandler{db: db}).createInitialCommit(libraryID.String(), orgID.String(), ownerID.String()); err == nil {
		t.Fatal("initial commit must not materialize a library row that was never created")
	}

	var storedLibraryID string
	err := session.Query(`SELECT library_id FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&storedLibraryID)
	if !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("canonical library row must remain absent after a rejected retry: err=%v row=%q", err, storedLibraryID)
	}
}

// TestCreateInitialCommit_AcceptsGenuineLegacyNullRow is the control case for
// the previous test: a library row that genuinely predates migration 021
// (publication_state IS NULL, row present, no revocation witness) must still
// be initializable through the legacy compatibility retry. Closing the
// resurrection hole must not also break real legacy compatibility.
func TestCreateInitialCommit_AcceptsGenuineLegacyNullRow(t *testing.T) {
	db := initialCommitAuthorityDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := session.Query(`
		INSERT INTO libraries (org_id, library_id, owner_id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		orgID.String(), libraryID.String(), ownerID.String(), "initial-commit-legacy-null",
		now, now).Exec(); err != nil {
		t.Fatalf("seed legacy (pre-021) library: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM fs_objects WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM commits WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries_by_id WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
	})

	var publicationState *string
	if err := session.Query(`SELECT publication_state FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&publicationState); err != nil {
		t.Fatalf("read seeded legacy library: %v", err)
	}
	if publicationState != nil {
		t.Fatalf("seeded library publication_state = %q, want NULL", *publicationState)
	}

	commitID, err := (&SyncHandler{db: db}).createInitialCommit(libraryID.String(), orgID.String(), ownerID.String())
	if err != nil {
		t.Fatalf("legacy NULL row must still initialize via the compatibility retry: %v", err)
	}

	var headCommitID, gotState string
	if err := session.Query(`SELECT head_commit_id, publication_state FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&headCommitID, &gotState); err != nil {
		t.Fatalf("read initialized legacy library: %v", err)
	}
	if headCommitID != commitID {
		t.Fatalf("head_commit_id = %q, want %q", headCommitID, commitID)
	}
	if gotState != dbpkg.LibraryPublicationStateActive {
		t.Fatalf("publication_state = %q, want ACTIVE", gotState)
	}
}
