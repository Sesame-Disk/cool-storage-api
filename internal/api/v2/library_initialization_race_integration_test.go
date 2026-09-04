//go:build integration

package v2

import (
	"errors"
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

// TestInitializeLibraryFS_LoserCleanupPreservesWinnersSharedRoot reproduces
// the initializer race every consolidated audit flagged as the top blocker:
// the empty-root fs_object is content-addressed ("1\n[]" hashed), so every
// initializer of the same library computes the identical fs_id. A second
// InitializeLibraryFS call deterministically loses the HEAD CAS once the
// first has published (no goroutine timing needed to force the race), and
// its cleanup must delete only its own rejected commit -- never the shared
// root the winner's already-published commit depends on.
func TestInitializeLibraryFS_LoserCleanupPreservesWinnersSharedRoot(t *testing.T) {
	db := restoreGuardDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := session.Query(`
		INSERT INTO libraries (org_id, library_id, owner_id, name, created_at, updated_at, publication_state)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		orgID.String(), libraryID.String(), ownerID.String(), "initialize-shared-root",
		now, now, dbpkg.LibraryPublicationStateActive).Exec(); err != nil {
		t.Fatalf("seed active library: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM fs_objects WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM commits WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries_by_id WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
	})

	helper := NewFSHelper(db)

	if err := helper.InitializeLibraryFS(orgID.String(), libraryID.String(), ownerID.String(), "initialize-shared-root"); err != nil {
		t.Fatalf("first (winning) initializer must succeed: %v", err)
	}
	var winnerCommitID string
	if err := session.Query(`SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&winnerCommitID); err != nil {
		t.Fatalf("read published head after first initializer: %v", err)
	}

	if err := helper.InitializeLibraryFS(orgID.String(), libraryID.String(), ownerID.String(), "initialize-shared-root"); err == nil {
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

// TestInitializeLibraryFS_RejectsResurrectionOfRevokedAbsentLibrary covers the
// second blocker: a NULL publication_state in a rejected CAS is ambiguous
// between "genuine untouched pre-021 row" and "absent partition", and
// Cassandra itself cannot distinguish them. A library whose canonical row was
// already hard-deleted (row absent, durable revocation witness present) must
// not be resurrected as ACTIVE by a stale initializer racing behind the
// delete.
func TestInitializeLibraryFS_RejectsResurrectionOfRevokedAbsentLibrary(t *testing.T) {
	db := restoreGuardDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()

	// Simulate the post-hard-delete state directly: canonical row absent,
	// durable revocation witness present. Seeding this via
	// dbpkg.RevokeLibraryPublication would not reproduce it: that helper's own
	// legacy-NULL retry upserts a bare TERMINAL tombstone row for a library ID
	// that does not otherwise exist, which is a different (already-covered)
	// state -- see TestInitializeLibraryFS_RejectsTerminalPublicationAuthority.
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

	if err := NewFSHelper(db).InitializeLibraryFS(orgID.String(), libraryID.String(), ownerID.String(), "initialize-resurrection-guard"); err == nil {
		t.Fatal("initialization must not resurrect a revoked, hard-deleted library")
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

// TestInitializeLibraryFS_AcceptsGenuineLegacyNullRow is the control case for
// the previous test: a library row that genuinely predates migration 021
// (publication_state IS NULL, row present, no revocation witness) must still
// be initializable through the legacy compatibility retry. Closing the
// resurrection hole must not also break real legacy compatibility.
func TestInitializeLibraryFS_AcceptsGenuineLegacyNullRow(t *testing.T) {
	db := restoreGuardDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := session.Query(`
		INSERT INTO libraries (org_id, library_id, owner_id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		orgID.String(), libraryID.String(), ownerID.String(), "initialize-legacy-null",
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

	if err := NewFSHelper(db).InitializeLibraryFS(orgID.String(), libraryID.String(), ownerID.String(), "initialize-legacy-null"); err != nil {
		t.Fatalf("legacy NULL row must still initialize via the compatibility retry: %v", err)
	}

	var headCommitID, gotState string
	if err := session.Query(`SELECT head_commit_id, publication_state FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&headCommitID, &gotState); err != nil {
		t.Fatalf("read initialized legacy library: %v", err)
	}
	if headCommitID == "" {
		t.Fatal("head_commit_id must be set after successful legacy initialization")
	}
	if gotState != dbpkg.LibraryPublicationStateActive {
		t.Fatalf("publication_state = %q, want ACTIVE", gotState)
	}
}
