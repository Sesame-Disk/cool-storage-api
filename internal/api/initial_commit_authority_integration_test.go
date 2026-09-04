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

// TestCreateInitialCommit_RejectsGenuinelyAbsentLibraryRow proves the primary
// (only) CAS can never resurrect a library_id with no canonical row:
// `IF head_commit_id = null AND publication_state = ACTIVE` reads
// publication_state as NULL against an absent partition, which never equals
// 'ACTIVE', so the CAS is rejected and nothing is materialized. This holds
// regardless of whether a durable revocation witness happens to exist for
// that library_id -- createInitialCommit does not consult the witness table
// at all; only GC/repair's own retention logic does.
func TestCreateInitialCommit_RejectsGenuinelyAbsentLibraryRow(t *testing.T) {
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
		t.Fatalf("canonical library row must remain absent after a rejected initial commit: err=%v row=%q", err, storedLibraryID)
	}
}

// TestCreateInitialCommit_RejectsNullPublicationState is the deliberate
// inverse of what this codebase supported before the W2a pre-merge closure
// removed legacy-NULL compatibility (docs/CHANGELOG.md "W2a pre-merge closure
// round 3"): this is a clean-deploy codebase with no pre-existing dataset, so
// there is no genuine row whose publication_state predates that column, and a
// row observed with NULL publication_state must be rejected outright rather
// than silently promoted to ACTIVE. Seeds a row the same way a hypothetical
// legacy row would look (present, publication_state never set) purely to
// prove createInitialCommit refuses it -- not to claim this state is
// expected to occur in practice.
func TestCreateInitialCommit_RejectsNullPublicationState(t *testing.T) {
	db := initialCommitAuthorityDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := session.Query(`
		INSERT INTO libraries (org_id, library_id, owner_id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		orgID.String(), libraryID.String(), ownerID.String(), "initial-commit-null-publication-state",
		now, now).Exec(); err != nil {
		t.Fatalf("seed library with unset publication_state: %v", err)
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
		t.Fatalf("read seeded library: %v", err)
	}
	if publicationState != nil {
		t.Fatalf("seeded library publication_state = %q, want NULL", *publicationState)
	}

	if _, err := (&SyncHandler{db: db}).createInitialCommit(libraryID.String(), orgID.String(), ownerID.String()); err == nil {
		t.Fatal("initial commit must reject a library row with NULL publication_state, not promote it to ACTIVE")
	}

	var headCommitID string
	if err := session.Query(`SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&headCommitID); err != nil {
		t.Fatalf("read library after rejected initial commit: %v", err)
	}
	if headCommitID != "" {
		t.Fatalf("head_commit_id = %q, want empty after a rejected initial commit", headCommitID)
	}
}
