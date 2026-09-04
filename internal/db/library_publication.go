package db

import (
	"errors"
	"fmt"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

const (
	LibraryPublicationStateActive   = "ACTIVE"
	LibraryPublicationStateTerminal = "TERMINAL"
)

// ErrLibraryPublicationTerminal means the canonical library row is still
// visible but its authority to publish HEAD has been permanently revoked.
var ErrLibraryPublicationTerminal = errors.New("library publication authority is terminal")

func readPublicationStateFromCAS(state map[string]interface{}) (value string, legacyNull bool) {
	raw, ok := state["publication_state"]
	if !ok || raw == nil {
		return "", true
	}
	value, ok = raw.(string)
	if !ok {
		return fmt.Sprint(raw), false
	}
	return value, value == ""
}

func revokeLibraryPublicationState(session *gocql.Session, orgID, libraryID string, legacyNull bool) (bool, map[string]interface{}, error) {
	casState := map[string]interface{}{}
	if legacyNull {
		applied, err := session.Query(`
			UPDATE libraries SET publication_state = ?
			WHERE org_id = ? AND library_id = ?
			IF publication_state = null
		`, LibraryPublicationStateTerminal, orgID, libraryID).
			SerialConsistency(gocql.Serial).MapScanCAS(casState)
		return applied, casState, err
	}
	applied, err := session.Query(`
		UPDATE libraries SET publication_state = ?
		WHERE org_id = ? AND library_id = ?
		IF publication_state = ?
	`, LibraryPublicationStateTerminal, orgID, libraryID, LibraryPublicationStateActive).
		SerialConsistency(gocql.Serial).MapScanCAS(casState)
	return applied, casState, err
}

func writeLibraryPublicationRevocation(session *gocql.Session, orgID, libraryID string) error {
	return session.Query(`
		INSERT INTO library_publication_revocations (org_id, library_id, revoked_at)
		VALUES (?, ?, ?)
	`, orgID, libraryID, time.Now().UTC()).Consistency(gocql.EachQuorum).Exec()
}

// IsLibraryPublicationRevoked checks the durable witness left after a
// terminal library row is hard-deleted. EACH_QUORUM is deliberate: a local
// marker write must not authorize destructive repair from another DC before
// the witness is visible there.
func IsLibraryPublicationRevoked(session *gocql.Session, orgID, libraryID string) (bool, error) {
	if session == nil {
		return false, fmt.Errorf("database session not available")
	}
	var revokedLibraryID string
	err := session.Query(`
		SELECT library_id FROM library_publication_revocations
		WHERE org_id = ? AND library_id = ?
	`, orgID, libraryID).Consistency(gocql.EachQuorum).Scan(&revokedLibraryID)
	if errors.Is(err, gocql.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return revokedLibraryID != "", nil
}

func confirmLibraryPublicationTerminal(session *gocql.Session, orgID, libraryID string, originalErr error) error {
	casState := map[string]interface{}{}
	applied, err := session.Query(`
		UPDATE libraries SET publication_state = ?
		WHERE org_id = ? AND library_id = ?
		IF publication_state = ?
	`, LibraryPublicationStateTerminal, orgID, libraryID, LibraryPublicationStateTerminal).
		SerialConsistency(gocql.Serial).MapScanCAS(casState)
	if err == nil && applied {
		return writeLibraryPublicationRevocation(session, orgID, libraryID)
	}
	if err != nil {
		return errors.Join(originalErr, fmt.Errorf("confirm terminal library publication state: %w", err))
	}
	state, _ := readPublicationStateFromCAS(casState)
	return fmt.Errorf("%w: observed publication state %q", originalErr, state)
}

// RevokeLibraryPublication commits the terminal publication authority before
// a library hard-delete. It is idempotent and settles an ambiguous LWT before
// allowing the caller to remove the canonical row.
func RevokeLibraryPublication(session *gocql.Session, orgID, libraryID string) error {
	if session == nil {
		return fmt.Errorf("database session not available")
	}
	if revoked, err := IsLibraryPublicationRevoked(session, orgID, libraryID); err != nil {
		return fmt.Errorf("check library publication revocation: %w", err)
	} else if revoked {
		return nil
	}

	applied, casState, err := revokeLibraryPublicationState(session, orgID, libraryID, false)
	if err != nil {
		return confirmLibraryPublicationTerminal(session, orgID, libraryID, err)
	}
	if !applied {
		state, legacyNull := readPublicationStateFromCAS(casState)
		switch {
		case state == LibraryPublicationStateTerminal:
			// Another hard-delete attempt won the same serial domain.
		case legacyNull:
			applied, casState, err = revokeLibraryPublicationState(session, orgID, libraryID, true)
			if err != nil {
				return confirmLibraryPublicationTerminal(session, orgID, libraryID, err)
			}
			if !applied {
				state, _ = readPublicationStateFromCAS(casState)
				if state != LibraryPublicationStateTerminal {
					return fmt.Errorf("library publication state is %q, want ACTIVE or TERMINAL", state)
				}
			}
		default:
			return fmt.Errorf("library publication state is %q, want ACTIVE or TERMINAL", state)
		}
	}

	if err := writeLibraryPublicationRevocation(session, orgID, libraryID); err != nil {
		return fmt.Errorf("persist library publication revocation: %w", err)
	}
	return nil
}
