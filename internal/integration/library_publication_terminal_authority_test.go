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

	// Exercise the migration compatibility path before the terminal transition:
	// pre-021 rows have a NULL state and must be settled by the same SERIAL LWT.
	if err := session.Query(`
		DELETE publication_state FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Exec(); err != nil {
		t.Fatalf("clear publication state for legacy-row check: %v", err)
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
