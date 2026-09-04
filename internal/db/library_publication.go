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

func readPublicationStateFromCAS(state map[string]interface{}) string {
	raw, ok := state["publication_state"]
	if !ok || raw == nil {
		return ""
	}
	value, ok := raw.(string)
	if !ok {
		return fmt.Sprint(raw)
	}
	return value
}

func revokeLibraryPublicationState(session *gocql.Session, orgID, libraryID string) (bool, map[string]interface{}, error) {
	casState := map[string]interface{}{}
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
	state := readPublicationStateFromCAS(casState)
	return fmt.Errorf("%w: observed publication state %q", originalErr, state)
}

// RevokeLibraryPublication commits the terminal publication authority before
// a library hard-delete. It is idempotent and settles an ambiguous LWT before
// allowing the caller to remove the canonical row.
//
// This is a clean-deploy codebase (docs/CHANGELOG.md "W2a pre-merge closure
// round 3"): every libraries row is created with publication_state already
// set to ACTIVE (all production INSERT INTO libraries call sites set it
// explicitly), and this migration ships as part of the initial schema
// baseline rather than being rolled out against a live fleet with existing
// rows. There is no legacy row whose publication_state predates this column,
// so an observed NULL here is unambiguous: it means the canonical row is
// absent, not that it is an old row to promote. Callers are expected to have
// already confirmed the row exists (under a hard-delete lease or a
// just-prior read) before calling this.
func RevokeLibraryPublication(session *gocql.Session, orgID, libraryID string) error {
	if session == nil {
		return fmt.Errorf("database session not available")
	}
	if revoked, err := IsLibraryPublicationRevoked(session, orgID, libraryID); err != nil {
		return fmt.Errorf("check library publication revocation: %w", err)
	} else if revoked {
		return nil
	}

	applied, casState, err := revokeLibraryPublicationState(session, orgID, libraryID)
	if err != nil {
		return confirmLibraryPublicationTerminal(session, orgID, libraryID, err)
	}
	if !applied {
		state := readPublicationStateFromCAS(casState)
		if state != LibraryPublicationStateTerminal {
			return fmt.Errorf("library publication state is %q, want ACTIVE or TERMINAL", state)
		}
		// Already TERMINAL: another hard-delete attempt won the same serial
		// domain. Idempotent, fall through to (re)write the witness.
	}

	if err := writeLibraryPublicationRevocation(session, orgID, libraryID); err != nil {
		return fmt.Errorf("persist library publication revocation: %w", err)
	}
	return nil
}
