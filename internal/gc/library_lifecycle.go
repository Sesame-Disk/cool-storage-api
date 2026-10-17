package gc

import (
	"errors"
	"fmt"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

// ErrLibraryLifecycleOwnershipLost means no final mutation was authorized.
var ErrLibraryLifecycleOwnershipLost = errors.New("library lifecycle lease is no longer preparatory and owned")

func acquireLibraryLifecycleLease(session *gocql.Session, libraryID, token uuid.UUID) (bool, error) {
	now := time.Now().UTC()
	existing := map[string]interface{}{}
	applied, err := session.Query(`INSERT INTO gc_library_hard_delete_locks (library_id,started_at,heartbeat,lease_token) VALUES (?,?,?,?) IF NOT EXISTS USING TTL ?`, libraryID.String(), now, now, token.String(), hardDeleteLockTTLSeconds).SerialConsistency(gocql.Serial).MapScanCAS(existing)
	if err != nil || applied {
		return applied, err
	}
	if finalizing, _ := existing["finalizing"].(bool); finalizing {
		return false, nil
	}
	heartbeat, oldToken := parseCASTime(existing["heartbeat"]), parseCASUUID(existing["lease_token"])
	if heartbeat.IsZero() || oldToken == uuid.Nil || now.Sub(heartbeat) < hardDeleteLockStaleAfter {
		return false, nil
	}
	// Compare the heartbeat too: a renewal or promotion since the failed INSERT
	// invalidates the stale observation, even if the token itself did not change.
	return session.Query(`UPDATE gc_library_hard_delete_locks USING TTL ? SET started_at=?,heartbeat=?,lease_token=? WHERE library_id=? IF lease_token=? AND heartbeat=? AND finalizing=null`, hardDeleteLockTTLSeconds, now, now, token.String(), libraryID.String(), oldToken.String(), heartbeat).SerialConsistency(gocql.Serial).MapScanCAS(map[string]interface{}{})
}

func renewLibraryLifecycleLease(session *gocql.Session, libraryID, token uuid.UUID) (bool, error) {
	applied, err := session.Query(`UPDATE gc_library_hard_delete_locks USING TTL ? SET heartbeat = ?, lease_token = ? WHERE library_id=? IF lease_token=? AND finalizing=null`, hardDeleteLockTTLSeconds, time.Now().UTC(), token.String(), libraryID.String(), token.String()).SerialConsistency(gocql.Serial).MapScanCAS(map[string]interface{}{})
	if err != nil || applied {
		return applied, err
	}
	// A concurrent heartbeat must never put an expiry back on a final section.
	return session.Query(`UPDATE gc_library_hard_delete_locks USING TTL 0 SET heartbeat=?,lease_token=? WHERE library_id=? IF lease_token=? AND finalizing=true`, time.Now().UTC(), token.String(), libraryID.String(), token.String()).SerialConsistency(gocql.Serial).MapScanCAS(map[string]interface{}{})
}

func releaseLibraryLifecycleLease(session *gocql.Session, libraryID, token uuid.UUID) error {
	_, err := session.Query(`DELETE FROM gc_library_hard_delete_locks WHERE library_id=? IF lease_token=? AND finalizing=null`, libraryID.String(), token.String()).SerialConsistency(gocql.Serial).MapScanCAS(map[string]interface{}{})
	return err
}

// WithLibraryLifecycleMutation enters a non-expiring final section before any
// lifecycle writes. Only a still-owned preparatory lease can enter. No takeover
// or ordinary deferred release can remove it. The callback must return nil only
// after its writes have completed; it must not launch asynchronous mutations.
// Any callback/CAS error retains exclusion, including ambiguous write outcomes.
// A crashed owner requires quiesced, operator-assisted recovery; never time out
// this section or retry an unknown batch under a new owner.
func WithLibraryLifecycleMutation(session *gocql.Session, libraryID, token uuid.UUID, mutation func() error) error {
	if libraryID == uuid.Nil || token == uuid.Nil || mutation == nil {
		return fmt.Errorf("invalid library lifecycle mutation authority")
	}
	now := time.Now().UTC()
	applied, err := session.Query(`UPDATE gc_library_hard_delete_locks USING TTL 0 SET started_at=?,heartbeat=?,lease_token=?,finalizing=true WHERE library_id=? IF lease_token=? AND finalizing=null`, now, now, token.String(), libraryID.String(), token.String()).SerialConsistency(gocql.Serial).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return fmt.Errorf("enter final library section %s (outcome may be unknown; retain exclusion): %w", libraryID, err)
	}
	if !applied {
		return ErrLibraryLifecycleOwnershipLost
	}
	if err := mutation(); err != nil {
		return fmt.Errorf("final library mutation %s failed; durable exclusion retained for recovery: %w", libraryID, err)
	}
	applied, err = session.Query(`DELETE FROM gc_library_hard_delete_locks WHERE library_id=? IF lease_token=? AND finalizing=true`, libraryID.String(), token.String()).SerialConsistency(gocql.Serial).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return fmt.Errorf("complete final library section %s: %w", libraryID, err)
	}
	if !applied {
		return fmt.Errorf("final library section %s lost durable exclusion", libraryID)
	}
	return nil
}

func (s *CassandraStore) WithLibraryLifecycleMutation(libraryID, token uuid.UUID, mutation func() error) error {
	return WithLibraryLifecycleMutation(s.db.Session(), libraryID, token, mutation)
}
