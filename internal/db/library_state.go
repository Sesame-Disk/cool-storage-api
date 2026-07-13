package db

import (
	"errors"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

// ErrLibraryDeleted indicates the canonical libraries row still exists but the
// library has been soft-deleted and must be treated as unavailable for live
// reads and writes.
var ErrLibraryDeleted = errors.New("library deleted")

// LibraryState captures the canonical fields that write/read fences need in
// order to treat soft-deleted libraries as unavailable.
type LibraryState struct {
	OrgID                 string
	LibraryID             string
	OwnerID               string
	Name                  string
	Encrypted             bool
	BlockRepresentationID string
	HeadCommitID          string
	StorageClass          string
	DeletedAt             *time.Time
}

// ReadLibraryState loads the canonical libraries row for a known org/library
// pair, including deleted_at so callers can distinguish live vs soft-deleted.
func ReadLibraryState(session *gocql.Session, orgID, libraryID string) (LibraryState, error) {
	// A malformed org/library id can never match a row; treat it as "not found"
	// rather than letting gocql raise a UUID marshal error that callers surface
	// as HTTP 500 (hostile/fat-fingered ids must not 500).
	if _, err := uuid.Parse(orgID); err != nil {
		return LibraryState{}, gocql.ErrNotFound
	}
	if _, err := uuid.Parse(libraryID); err != nil {
		return LibraryState{}, gocql.ErrNotFound
	}

	state := LibraryState{
		OrgID:     orgID,
		LibraryID: libraryID,
	}

	var deletedAt time.Time
	if err := session.Query(`
		SELECT owner_id, name, encrypted, block_representation_id, head_commit_id, storage_class, deleted_at
		FROM libraries
		WHERE org_id = ? AND library_id = ?
	`, orgID, libraryID).Scan(
		&state.OwnerID,
		&state.Name,
		&state.Encrypted,
		&state.BlockRepresentationID,
		&state.HeadCommitID,
		&state.StorageClass,
		&deletedAt,
	); err != nil {
		return LibraryState{}, err
	}

	if !deletedAt.IsZero() {
		deletedCopy := deletedAt
		state.DeletedAt = &deletedCopy
	}

	return state, nil
}

// ReadLiveLibraryState returns the canonical library row only when the library
// is still live. Soft-deleted libraries are reported via ErrLibraryDeleted.
func ReadLiveLibraryState(session *gocql.Session, orgID, libraryID string) (LibraryState, error) {
	state, err := ReadLibraryState(session, orgID, libraryID)
	if err != nil {
		return LibraryState{}, err
	}
	if state.DeletedAt != nil {
		return LibraryState{}, ErrLibraryDeleted
	}
	return state, nil
}

// ResolveLiveLibraryStateByID resolves the org partition through libraries_by_id
// and then returns the canonical live library row.
func ResolveLiveLibraryStateByID(session *gocql.Session, libraryID string) (LibraryState, error) {
	var orgID string
	if err := session.Query(`
		SELECT org_id FROM libraries_by_id WHERE library_id = ?
	`, libraryID).Scan(&orgID); err != nil {
		return LibraryState{}, err
	}

	return ReadLiveLibraryState(session, orgID, libraryID)
}
