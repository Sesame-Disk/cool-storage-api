package db

import (
	"fmt"
	"sync"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

const hardDeleteLeaseTTLSeconds = 21600

const (
	HardDeleteLeaseHeartbeatInterval = 30 * time.Minute
	HardDeleteLeaseStaleAfter        = 3 * HardDeleteLeaseHeartbeatInterval
)

// HardDeleteLeaseTarget selects one of the fixed lifecycle-lock tables.
type HardDeleteLeaseTarget string

const (
	HardDeleteLeaseUser    HardDeleteLeaseTarget = "user"
	HardDeleteLeaseLibrary HardDeleteLeaseTarget = "library"
	HardDeleteLeaseOrg     HardDeleteLeaseTarget = "org"
)

func hardDeleteLeaseTable(target HardDeleteLeaseTarget) (tableName, keyColumn string, err error) {
	switch target {
	case HardDeleteLeaseUser:
		return "gc_user_hard_delete_locks", "user_id", nil
	case HardDeleteLeaseLibrary:
		return "gc_library_hard_delete_locks", "library_id", nil
	case HardDeleteLeaseOrg:
		return "gc_org_hard_delete_locks", "org_id", nil
	default:
		return "", "", fmt.Errorf("unknown hard-delete lease target %q", target)
	}
}

// AcquireHardDeleteLease acquires a tokenized lifecycle lease, taking over an
// abandoned owner only after staleAfter. Callers must renew and release with the
// same token.
func AcquireHardDeleteLease(session *gocql.Session, target HardDeleteLeaseTarget, targetID, leaseToken uuid.UUID, staleAfter time.Duration) (bool, error) {
	tableName, keyColumn, err := hardDeleteLeaseTable(target)
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	existing := map[string]interface{}{}
	applied, err := session.Query(fmt.Sprintf(`
		INSERT INTO %s (%s, started_at, heartbeat, lease_token)
		VALUES (?, ?, ?, ?) IF NOT EXISTS
	`, tableName, keyColumn), targetID.String(), now, now, leaseToken.String()).MapScanCAS(existing)
	if err != nil || applied {
		return applied, err
	}

	heartbeat, heartbeatOK := leaseTime(existing["heartbeat"])
	existingToken, tokenOK := leaseUUID(existing["lease_token"])
	if !heartbeatOK || !tokenOK || now.Sub(heartbeat) < staleAfter {
		return false, nil
	}

	return session.Query(fmt.Sprintf(`
		UPDATE %s USING TTL %d
		SET started_at = ?, heartbeat = ?, lease_token = ?
		WHERE %s = ? IF lease_token = ?
	`, tableName, hardDeleteLeaseTTLSeconds, keyColumn), now, now, leaseToken.String(), targetID.String(), existingToken.String()).MapScanCAS(map[string]interface{}{})
}

// RenewHardDeleteLease refreshes a lease only if leaseToken still owns it.
func RenewHardDeleteLease(session *gocql.Session, target HardDeleteLeaseTarget, targetID, leaseToken uuid.UUID) (bool, error) {
	tableName, keyColumn, err := hardDeleteLeaseTable(target)
	if err != nil {
		return false, err
	}
	return session.Query(fmt.Sprintf(`
		UPDATE %s USING TTL %d
		SET heartbeat = ?, lease_token = ?
		WHERE %s = ? IF lease_token = ?
	`, tableName, hardDeleteLeaseTTLSeconds, keyColumn), time.Now().UTC(), leaseToken.String(), targetID.String(), leaseToken.String()).MapScanCAS(map[string]interface{}{})
}

// ReleaseHardDeleteLease removes a lease only if leaseToken still owns it.
func ReleaseHardDeleteLease(session *gocql.Session, target HardDeleteLeaseTarget, targetID, leaseToken uuid.UUID) error {
	tableName, keyColumn, err := hardDeleteLeaseTable(target)
	if err != nil {
		return err
	}
	applied, err := session.Query(fmt.Sprintf(`
		DELETE FROM %s WHERE %s = ? IF lease_token = ?
	`, tableName, keyColumn), targetID.String(), leaseToken.String()).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return err
	}
	if !applied {
		return fmt.Errorf("%s lifecycle lease for %s was lost before release", target, targetID)
	}
	return nil
}

// RenewingHardDeleteLease owns a lifecycle lease and refreshes its heartbeat
// until Close. Close conditionally releases the same token.
type RenewingHardDeleteLease struct {
	session    *gocql.Session
	target     HardDeleteLeaseTarget
	targetID   uuid.UUID
	leaseToken uuid.UUID
	stop       chan struct{}
	done       chan struct{}
	closeOnce  sync.Once

	mu  sync.Mutex
	err error
}

// AcquireRenewingHardDeleteLease acquires a lease and starts heartbeat renewal.
func AcquireRenewingHardDeleteLease(session *gocql.Session, target HardDeleteLeaseTarget, targetID uuid.UUID, staleAfter, heartbeatInterval time.Duration) (*RenewingHardDeleteLease, bool, error) {
	leaseToken := uuid.New()
	applied, err := AcquireHardDeleteLease(session, target, targetID, leaseToken, staleAfter)
	if err != nil || !applied {
		return nil, applied, err
	}
	lease := &RenewingHardDeleteLease{
		session: session, target: target, targetID: targetID, leaseToken: leaseToken,
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	go lease.heartbeat(heartbeatInterval)
	return lease, true, nil
}

func (l *RenewingHardDeleteLease) heartbeat(interval time.Duration) {
	defer close(l.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			applied, err := RenewHardDeleteLease(l.session, l.target, l.targetID, l.leaseToken)
			if err != nil {
				l.setErr(err)
				return
			}
			if !applied {
				l.setErr(fmt.Errorf("%s lifecycle lease for %s was lost", l.target, l.targetID))
				return
			}
		}
	}
}

func (l *RenewingHardDeleteLease) setErr(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err == nil {
		l.err = err
	}
}

// Check reports a heartbeat or ownership failure.
func (l *RenewingHardDeleteLease) Check() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

// Close stops renewal and conditionally releases the lease.
func (l *RenewingHardDeleteLease) Close() error {
	l.closeOnce.Do(func() {
		close(l.stop)
		<-l.done
		if err := ReleaseHardDeleteLease(l.session, l.target, l.targetID, l.leaseToken); err != nil {
			l.setErr(err)
		}
	})
	return l.Check()
}

func leaseUUID(value interface{}) (uuid.UUID, bool) {
	switch typed := value.(type) {
	case uuid.UUID:
		return typed, typed != uuid.Nil
	case string:
		parsed, err := uuid.Parse(typed)
		return parsed, err == nil && parsed != uuid.Nil
	case []byte:
		parsed, err := uuid.FromBytes(typed)
		return parsed, err == nil && parsed != uuid.Nil
	case fmt.Stringer:
		parsed, err := uuid.Parse(typed.String())
		return parsed, err == nil && parsed != uuid.Nil
	default:
		return uuid.Nil, false
	}
}

func leaseTime(value interface{}) (time.Time, bool) {
	switch typed := value.(type) {
	case time.Time:
		return typed, !typed.IsZero()
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, typed)
		if err != nil {
			parsed, err = time.Parse(time.RFC3339, typed)
		}
		return parsed, err == nil && !parsed.IsZero()
	default:
		return time.Time{}, false
	}
}
