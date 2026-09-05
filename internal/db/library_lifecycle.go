package db

import (
	"errors"
	"fmt"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// SoftDeleteLibraryCanonical makes libraries.deleted_at the authoritative
// lifecycle transition. The marker, projections, and reconciliation rows are
// derived state and may be written by a later LoggedBatch without allowing a
// partially-visible batch to hide an ACTIVE canonical row.
//
// A failed CAS is settled with a SERIAL read before returning. If the
// transition did commit but the client lost the response, the observed
// deleted_at is returned and callers can safely finish the derived writes.
func SoftDeleteLibraryCanonical(session *gocql.Session, orgID, libraryID, deletedBy string, deletedAt time.Time) (time.Time, error) {
	if session == nil {
		return time.Time{}, fmt.Errorf("database session not available")
	}
	if deletedAt.IsZero() {
		return time.Time{}, fmt.Errorf("soft-delete timestamp is required")
	}

	casState := map[string]interface{}{}
	applied, err := session.Query(`
		UPDATE libraries SET deleted_at = ?, deleted_by = ?, updated_at = ?
		WHERE org_id = ? AND library_id = ?
		IF deleted_at = null AND publication_state = ?`,
		deletedAt, deletedBy, deletedAt, orgID, libraryID, LibraryPublicationStateActive,
	).SerialConsistency(gocql.Serial).MapScanCAS(casState)
	if err == nil && applied {
		return deletedAt, nil
	}

	return settleSoftDeleteCanonical(session, orgID, libraryID, err)
}

func settleSoftDeleteCanonical(session *gocql.Session, orgID, libraryID string, originalErr error) (time.Time, error) {
	var deletedAt time.Time
	var publicationState *string
	err := session.Query(`
		SELECT deleted_at, publication_state FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, libraryID).Consistency(gocql.Serial).Scan(&deletedAt, &publicationState)
	if err != nil {
		if originalErr != nil {
			return time.Time{}, errors.Join(originalErr, fmt.Errorf("settle soft-delete canonical state: %w", err))
		}
		return time.Time{}, fmt.Errorf("settle soft-delete canonical state: %w", err)
	}
	if publicationState == nil || *publicationState != LibraryPublicationStateActive {
		state := ""
		if publicationState != nil {
			state = *publicationState
		}
		settledErr := fmt.Errorf("library publication state is %q, want ACTIVE", state)
		if originalErr != nil {
			return time.Time{}, errors.Join(originalErr, settledErr)
		}
		return time.Time{}, settledErr
	}
	if !deletedAt.IsZero() {
		return deletedAt, nil
	}

	settledErr := fmt.Errorf("soft-delete canonical CAS did not apply")
	if originalErr != nil {
		return time.Time{}, errors.Join(originalErr, settledErr)
	}
	return time.Time{}, settledErr
}
