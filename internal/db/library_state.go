package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
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
	PublicationState      string
}

// ReadLibraryState loads the canonical libraries row for a known org/library
// pair, including deleted_at so callers can distinguish live vs soft-deleted.
func ReadLibraryState(session *gocql.Session, orgID, libraryID string) (LibraryState, error) {
	return ReadLibraryStateContext(context.Background(), session, orgID, libraryID)
}

// ReadLibraryStateContext is ReadLibraryState bound to ctx. Request-scoped
// callers use it so metadata work stops when the client disconnects or its
// preparation deadline expires.
func ReadLibraryStateContext(ctx context.Context, session *gocql.Session, orgID, libraryID string) (LibraryState, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	state := LibraryState{
		OrgID:     orgID,
		LibraryID: libraryID,
	}

	var deletedAt time.Time
	var publicationState *string
	if err := session.Query(`
		SELECT owner_id, name, encrypted, block_representation_id, head_commit_id, storage_class, deleted_at, publication_state
		FROM libraries
		WHERE org_id = ? AND library_id = ?
	`, orgID, libraryID).WithContext(ctx).Scan(
		&state.OwnerID,
		&state.Name,
		&state.Encrypted,
		&state.BlockRepresentationID,
		&state.HeadCommitID,
		&state.StorageClass,
		&deletedAt,
		&publicationState,
	); err != nil {
		return LibraryState{}, err
	}
	// Clean-deploy invariant (docs/CHANGELOG.md "W2a pre-merge closure round
	// 3"): every libraries row is created with publication_state already
	// ACTIVE, so any other observed value -- NULL, empty, or unrecognized --
	// is an invalid row, not a legacy one to tolerate. Fail closed rather than
	// silently treating it as ACTIVE.
	switch {
	case publicationState != nil && *publicationState == LibraryPublicationStateActive:
		state.PublicationState = LibraryPublicationStateActive
	case publicationState != nil && *publicationState == LibraryPublicationStateTerminal:
		state.PublicationState = LibraryPublicationStateTerminal
	default:
		value := ""
		if publicationState != nil {
			value = *publicationState
		}
		return LibraryState{}, fmt.Errorf("library publication state is %q, want ACTIVE or TERMINAL", value)
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
	return ReadLiveLibraryStateContext(context.Background(), session, orgID, libraryID)
}

// ReadLiveLibraryStateContext is ReadLiveLibraryState bound to ctx.
func ReadLiveLibraryStateContext(ctx context.Context, session *gocql.Session, orgID, libraryID string) (LibraryState, error) {
	state, err := ReadLibraryStateContext(ctx, session, orgID, libraryID)
	if err != nil {
		return LibraryState{}, err
	}
	if state.DeletedAt != nil {
		return LibraryState{}, ErrLibraryDeleted
	}
	if state.PublicationState == LibraryPublicationStateTerminal {
		return LibraryState{}, ErrLibraryPublicationTerminal
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
