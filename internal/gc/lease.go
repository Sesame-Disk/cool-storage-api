package gc

import (
	"context"
	"errors"
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

	// TryTakeoverIfStale attempts to seize the lease from a previous owner
	// whose heartbeat is older than the given staleness window. Use this
	// only on admin-triggered paths (not the worker hot loop) to recover
	// from a crashed leader without waiting for the lease TTL to expire.
	// Returns (true, nil) only if this instance now holds the lease.
	TryTakeoverIfStale(ctx context.Context, staleness time.Duration) (bool, error)

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

func (l *cassandraLeaderLease) TryTakeoverIfStale(ctx context.Context, staleness time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if staleness <= 0 {
		return false, nil
	}
	var currentInstanceID string
	var heartbeat time.Time
	err := l.session.Query(`
		SELECT instance_id, heartbeat FROM gc_leases WHERE role = ?
	`, l.role).WithContext(ctx).Scan(&currentInstanceID, &heartbeat)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			// Row already gone (TTL expired between checks) — fall back to
			// the normal acquire path so the caller still ends up holding
			// the lease without an extra round-trip from their side.
			return l.TryAcquireOrRenew(ctx)
		}
		return false, fmt.Errorf("gc leader lease read for takeover failed: %w", err)
	}
	if currentInstanceID == l.instanceID {
		// Already ours — refresh and report success.
		return l.TryAcquireOrRenew(ctx)
	}
	if l.now().Sub(heartbeat) < staleness {
		// Previous owner is still healthy — never steal a fresh lease.
		return false, nil
	}
	now := l.now()
	applied, err := l.session.Query(`
		UPDATE gc_leases USING TTL ?
		SET instance_id = ?, heartbeat = ?
		WHERE role = ? IF instance_id = ? AND heartbeat = ?
	`, l.ttlSeconds, l.instanceID, now, l.role, currentInstanceID, heartbeat).MapScanCAS(map[string]interface{}{})
	if err != nil {
		l.isLeader.Store(false)
		return false, fmt.Errorf("gc leader lease takeover failed: %w", err)
	}
	if applied {
		log.Printf("[GC] Took over stale lease from %q (heartbeat age %v)", currentInstanceID, l.now().Sub(heartbeat))
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
