//go:build integration

package integration

import (
	"errors"
	"fmt"
	"testing"
	"time"

	v2api "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

func TestLibraryPublicationTerminalAuthorityBlocksHEADAndSurvivesHardDelete(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-publication-terminal-%d", time.Now().UnixNano()))
	orgID := resolveOrgID(t, repoID)
	session := database.Session()

	state, err := dbpkg.ReadLibraryState(session, orgID, repoID)
	if err != nil {
		t.Fatalf("read newly created library state: %v", err)
	}
	if state.PublicationState != dbpkg.LibraryPublicationStateActive {
		t.Fatalf("new library publication state = %q, want ACTIVE", state.PublicationState)
	}

	if err := dbpkg.RevokeLibraryPublication(session, orgID, repoID); err != nil {
		t.Fatalf("revoke library publication authority: %v", err)
	}
	state, err = dbpkg.ReadLibraryState(session, orgID, repoID)
	if err != nil {
		t.Fatalf("read terminal library state: %v", err)
	}
	if state.PublicationState != dbpkg.LibraryPublicationStateTerminal {
		t.Fatalf("revoked library publication state = %q, want TERMINAL", state.PublicationState)
	}

	casState := map[string]interface{}{}
	applied, err := session.Query(`
		UPDATE libraries SET head_commit_id = ?
		WHERE org_id = ? AND library_id = ?
		IF head_commit_id = ? AND publication_state = ?
	`, state.HeadCommitID, orgID, repoID, state.HeadCommitID, dbpkg.LibraryPublicationStateActive).
		SerialConsistency(gocql.Serial).MapScanCAS(casState)
	if err != nil {
		t.Fatalf("terminal HEAD CAS: %v", err)
	}
	if applied {
		t.Fatal("HEAD CAS applied after publication authority became terminal")
	}

	// The raw CQL above proves the CAS itself is guarded; this proves the
	// production writer path surfaces that rejection as a distinct,
	// non-retryable error instead of the ordinary (retryable)
	// ErrLibraryHeadConflict a live HEAD race returns. Misclassifying it
	// would make bounded retry loops (e.g. seafhttp.go's
	// commitUploadedFileMultiBlock) burn every attempt against a library
	// that will never accept a HEAD CAS again.
	updateErr := v2api.NewFSHelper(database).UpdateLibraryHead(orgID, repoID, state.HeadCommitID, state.HeadCommitID)
	if !errors.Is(updateErr, dbpkg.ErrLibraryPublicationTerminal) {
		t.Fatalf("UpdateLibraryHead() error = %v, want db.ErrLibraryPublicationTerminal", updateErr)
	}
	if errors.Is(updateErr, v2api.ErrLibraryHeadConflict) {
		t.Fatalf("UpdateLibraryHead() error = %v, must not also classify as the retryable ErrLibraryHeadConflict", updateErr)
	}

	libraryUUID, err := uuid.Parse(repoID)
	if err != nil {
		t.Fatalf("parse library UUID: %v", err)
	}
	orgUUID, err := uuid.Parse(orgID)
	if err != nil {
		t.Fatalf("parse organization UUID: %v", err)
	}
	if err := gcpkg.NewCassandraStore(database).HardDeleteLibrary(orgUUID, libraryUUID); err != nil {
		t.Fatalf("hard-delete terminal library: %v", err)
	}

	if revoked, err := dbpkg.IsLibraryPublicationRevoked(session, orgID, repoID); err != nil {
		t.Fatalf("read post-delete revocation witness: %v", err)
	} else if !revoked {
		t.Fatal("post-delete revocation witness is missing")
	}
	var storedLibraryID string
	err = session.Query(`
		SELECT library_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&storedLibraryID)
	if !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("canonical library row error = %v, want ErrNotFound", err)
	}
}

// TestRevokeLibraryPublicationRejectsGenuinelyAbsentLibrary pins a defense-in-
// depth invariant: RevokeLibraryPublication is exported with no guarantee in
// its own signature that the caller already confirmed the row exists (every
// production caller -- the API and GC hard-delete paths, rollbackNewLibrary
// -- does confirm it first, so this is not a currently reachable call path).
// Its single CAS conditions on `publication_state = ACTIVE`; an absent
// partition reads that column as NULL, which never equals 'ACTIVE', so the
// CAS is rejected and the function must return an error rather than
// materializing a stub row or a revocation witness for a library_id that was
// never created.
func TestRevokeLibraryPublicationRejectsGenuinelyAbsentLibrary(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	session := database.Session()
	orgID, libraryID := uuid.New().String(), uuid.New().String()

	if err := dbpkg.RevokeLibraryPublication(session, orgID, libraryID); err == nil {
		t.Fatal("RevokeLibraryPublication must fail against a library_id with no canonical row, not materialize a stub TERMINAL row")
	}

	var storedLibraryID string
	err := session.Query(`
		SELECT library_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, libraryID).Scan(&storedLibraryID)
	if !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("canonical library row must remain absent after a rejected revoke: err=%v row=%q", err, storedLibraryID)
	}
	if revoked, err := dbpkg.IsLibraryPublicationRevoked(session, orgID, libraryID); err != nil {
		t.Fatalf("read revocation witness: %v", err)
	} else if revoked {
		t.Fatal("a rejected revoke against a genuinely absent library must not write a revocation witness")
	}
}
