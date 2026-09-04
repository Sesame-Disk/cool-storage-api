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

// TestInitializeLibraryFS_RejectsGenuinelyAbsentLibraryRow proves the primary
// (only) CAS can never resurrect a library_id with no canonical row:
// `IF head_commit_id = null AND publication_state = ACTIVE` reads
// publication_state as NULL against an absent partition, which never equals
// 'ACTIVE', so the CAS is rejected and nothing is materialized. This holds
// regardless of whether a durable revocation witness happens to exist for
// that library_id -- InitializeLibraryFS does not consult the witness table
// at all; only GC/repair's own retention logic does.
func TestInitializeLibraryFS_RejectsGenuinelyAbsentLibraryRow(t *testing.T) {
	db := restoreGuardDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()

	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM fs_objects WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM commits WHERE library_id = ?`, libraryID.String()).Exec()
		_ = session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID.String(), libraryID.String()).Exec()
	})

	if err := NewFSHelper(db).InitializeLibraryFS(orgID.String(), libraryID.String(), ownerID.String(), "initialize-absent-row-guard"); err == nil {
		t.Fatal("initialization must not materialize a library row that was never created")
	}

	var storedLibraryID string
	err := session.Query(`SELECT library_id FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&storedLibraryID)
	if !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("canonical library row must remain absent after a rejected initialization: err=%v row=%q", err, storedLibraryID)
	}
}

// TestInitializeLibraryFS_RejectsNullPublicationState is the deliberate
// inverse of what this codebase supported before the W2a pre-merge closure
// removed legacy-NULL compatibility (docs/CHANGELOG.md "W2a pre-merge closure
// round 3"): this is a clean-deploy codebase with no pre-existing dataset, so
// there is no genuine row whose publication_state predates that column, and a
// row observed with NULL publication_state must be rejected outright rather
// than silently promoted to ACTIVE. Seeds a row the same way a hypothetical
// legacy row would look (present, publication_state never set) purely to
// prove InitializeLibraryFS refuses it -- not to claim this state is expected
// to occur in practice.
func TestInitializeLibraryFS_RejectsNullPublicationState(t *testing.T) {
	db := restoreGuardDBForTest(t)
	session := db.Session()
	orgID, libraryID, ownerID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Millisecond)

	if err := session.Query(`
		INSERT INTO libraries (org_id, library_id, owner_id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		orgID.String(), libraryID.String(), ownerID.String(), "initialize-null-publication-state",
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

	if err := NewFSHelper(db).InitializeLibraryFS(orgID.String(), libraryID.String(), ownerID.String(), "initialize-null-publication-state"); err == nil {
		t.Fatal("initialization must reject a library row with NULL publication_state, not promote it to ACTIVE")
	}

	var headCommitID string
	if err := session.Query(`SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID.String(), libraryID.String()).Scan(&headCommitID); err != nil {
		t.Fatalf("read library after rejected initialization: %v", err)
	}
	if headCommitID != "" {
		t.Fatalf("head_commit_id = %q, want empty after a rejected initialization", headCommitID)
	}
}
