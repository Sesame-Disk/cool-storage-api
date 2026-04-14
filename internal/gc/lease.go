package gc

import (
	"context"
	"fmt"
	"os"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

const (
	gcLeaderRole      = "gc"
	minLeaderLeaseTTL = 90 * time.Second
)

type leaderLease interface {
	TryAcquireOrRenew(ctx context.Context) (bool, error)
}

type cassandraLeaderLease struct {
	session    *gocql.Session
	role       string
	instanceID string
	ttlSeconds int
	now        func() time.Time
}

func newCassandraLeaderLease(session *gocql.Session, role string, workerInterval time.Duration) leaderLease {
	if session == nil {
		return nil
	}
	ttl := workerInterval * 3
	if ttl < minLeaderLeaseTTL {
		ttl = minLeaderLeaseTTL
	}
	return &cassandraLeaderLease{
		session:    session,
		role:       role,
		instanceID: newGCInstanceID(),
		ttlSeconds: int(ttl / time.Second),
		now:        time.Now,
	}
}

func newGCInstanceID() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown-host"
	}
	return fmt.Sprintf("%s-%d-%d", hostname, os.Getpid(), time.Now().UnixNano())
}

func (l *cassandraLeaderLease) TryAcquireOrRenew(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	now := l.now()
	applied, err := l.session.Query(`
		INSERT INTO gc_leases (role, instance_id, heartbeat)
		VALUES (?, ?, ?) USING TTL ? IF NOT EXISTS
	`, l.role, l.instanceID, now, l.ttlSeconds).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return false, fmt.Errorf("gc leader lease insert failed: %w", err)
	}
	if applied {
		return true, nil
	}
	applied, err = l.session.Query(`
		UPDATE gc_leases USING TTL ?
		SET instance_id = ?, heartbeat = ?
		WHERE role = ? IF instance_id = ?
	`, l.ttlSeconds, l.instanceID, now, l.role, l.instanceID).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return false, fmt.Errorf("gc leader lease renew failed: %w", err)
	}
	return applied, nil
}
