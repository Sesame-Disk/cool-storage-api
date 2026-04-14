package gc

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync/atomic"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

const (
	gcLeaderRole      = "gc"
	minLeaderLeaseTTL = 90 * time.Second
)

type leaderLease interface {
	// TryAcquireOrRenew attempts to claim or extend the lease. Thread-safe.
	TryAcquireOrRenew(ctx context.Context) (bool, error)

	// Release explicitly deletes the lease row so another replica can take
	// over immediately instead of waiting for TTL expiry. Best-effort.
	Release(ctx context.Context)

	// IsLeader returns the most recently observed leadership state without
	// hitting the database. Use this for fast checks in hot paths.
	IsLeader() bool
}

type cassandraLeaderLease struct {
	session    *gocql.Session
	role       string
	instanceID string
	ttlSeconds int
	now        func() time.Time

	// isLeader caches the result of the last TryAcquireOrRenew so that
	// hasLeadership() can be a cheap read between renewal ticks.
	isLeader atomic.Bool
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
		VALUES (?, ?, ?) IF NOT EXISTS USING TTL ?
	`, l.role, l.instanceID, now, l.ttlSeconds).MapScanCAS(map[string]interface{}{})
	if err != nil {
		l.isLeader.Store(false)
		return false, fmt.Errorf("gc leader lease insert failed: %w", err)
	}
	if applied {
		l.isLeader.Store(true)
		return true, nil
	}
	applied, err = l.session.Query(`
		UPDATE gc_leases USING TTL ?
		SET instance_id = ?, heartbeat = ?
		WHERE role = ? IF instance_id = ?
	`, l.ttlSeconds, l.instanceID, now, l.role, l.instanceID).MapScanCAS(map[string]interface{}{})
	if err != nil {
		l.isLeader.Store(false)
		return false, fmt.Errorf("gc leader lease renew failed: %w", err)
	}
	l.isLeader.Store(applied)
	return applied, nil
}

func (l *cassandraLeaderLease) Release(ctx context.Context) {
	if err := l.session.Query(`
		DELETE FROM gc_leases WHERE role = ? IF instance_id = ?
	`, l.role, l.instanceID).WithContext(ctx).Exec(); err != nil {
		log.Printf("[GC] Best-effort lease release failed: %v", err)
	}
	l.isLeader.Store(false)
}

func (l *cassandraLeaderLease) IsLeader() bool {
	return l.isLeader.Load()
}
