package v2

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// Password rate-limit thresholds.
//
// The policy is intentionally simple and persistent:
//   - Up to softFailureThreshold failures: no penalty.
//   - After softFailureThreshold: block briefly, doubling the block window
//     for each additional step of softFailureThreshold failures, capped.
//
// Persistence across restarts is what buys us protection against an attacker
// who scripts thousands of attempts — an in-memory limiter resets with every
// deploy, which makes it nearly useless for a patient adversary.
const (
	softFailureThreshold = 5
	initialBlockWindow   = 1 * time.Minute
	maxBlockWindow       = 30 * time.Minute
	passwordAttemptsTTL  = 24 * time.Hour
	maxCASRetries        = 8
)

// ErrPasswordRateLimited is returned when the caller is currently blocked.
var ErrPasswordRateLimited = errors.New("password rate limited")

// passwordAttemptsStore is the minimum surface a backend must implement.
// Abstracted so unit tests can use an in-memory fake without Cassandra.
type passwordAttemptsStore interface {
	Load(repoID, actorKey string) (failures int, blockedUntil time.Time, err error)
	Save(repoID, actorKey string, failures int, firstFailure, lastFailure, blockedUntil time.Time) error
	Reset(repoID, actorKey string) error
}

type atomicPasswordFailureRecorder interface {
	RecordFailure(repoID, actorKey string, now time.Time) (failures int, blockedUntil time.Time, err error)
}

// PasswordRateLimiter guards set-password endpoints against online brute force.
// It tracks failures per (repo_id, actor_key) — where actor_key is typically
// the authenticated user_id or the client IP — in a Cassandra-backed store so
// that the limit survives process restarts.
type PasswordRateLimiter struct {
	store passwordAttemptsStore
	now   func() time.Time
}

// NewPasswordRateLimiter builds a limiter backed by the libraries database.
// Returns nil if db is nil so callers can operate in a degraded but safe mode
// during early bootstrap (no DB → no attempts possible yet).
func NewPasswordRateLimiter(database *db.DB) *PasswordRateLimiter {
	if database == nil {
		return nil
	}
	return &PasswordRateLimiter{
		store: &cassandraAttemptsStore{session: database.Session()},
		now:   time.Now,
	}
}

// Check asserts the caller is allowed to attempt a password right now.
// Returns a positive retryAfter and ErrPasswordRateLimited while a block is
// active. Other errors (e.g. DB unreachable) are returned so the caller can
// fail open or closed as appropriate.
func (l *PasswordRateLimiter) Check(repoID, actorKey string) (time.Duration, error) {
	if l == nil {
		return 0, nil
	}
	_, blockedUntil, err := l.store.Load(repoID, actorKey)
	if err != nil {
		return 0, err
	}
	now := l.now()
	if !blockedUntil.IsZero() && blockedUntil.After(now) {
		return blockedUntil.Sub(now), ErrPasswordRateLimited
	}
	return 0, nil
}

// RecordFailure increments the counter and, once the soft threshold is
// crossed, sets an exponentially growing block window. Callers do not need to
// call Check first; invoking this after every wrong-password response is
// sufficient.
func (l *PasswordRateLimiter) RecordFailure(repoID, actorKey string) error {
	if l == nil {
		return nil
	}
	if atomicStore, ok := l.store.(atomicPasswordFailureRecorder); ok {
		_, _, err := atomicStore.RecordFailure(repoID, actorKey, l.now())
		return err
	}
	failures, _, err := l.store.Load(repoID, actorKey)
	if err != nil {
		return err
	}
	failures++
	now := l.now()
	firstFailure := now
	if failures == 1 {
		firstFailure = now
	}

	blockedUntil := blockedUntilForFailures(failures, now)
	return l.store.Save(repoID, actorKey, failures, firstFailure, now, blockedUntil)
}

// RecordSuccess clears the failure counter for a (repo, actor) pair.
// Must be called on successful password verification so that a legitimate
// user does not accumulate state after a forgetful moment.
func (l *PasswordRateLimiter) RecordSuccess(repoID, actorKey string) error {
	if l == nil {
		return nil
	}
	return l.store.Reset(repoID, actorKey)
}

// ── Cassandra-backed store ────────────────────────────────────────────────

type cassandraAttemptsStore struct {
	session *gocql.Session
}

func blockedUntilForFailures(failures int, now time.Time) time.Time {
	if failures < softFailureThreshold {
		return time.Time{}
	}
	steps := (failures - softFailureThreshold) / softFailureThreshold
	window := initialBlockWindow << steps
	if window <= 0 || window > maxBlockWindow {
		window = maxBlockWindow
	}
	return now.Add(window)
}

func (s *cassandraAttemptsStore) Load(repoID, actorKey string) (int, time.Time, error) {
	var failures int
	var blockedUntil *time.Time
	err := s.session.Query(`
		SELECT failure_count, blocked_until
		FROM encrypted_repo_password_failures
		WHERE repo_id = ? AND actor_key = ?
	`, repoID, actorKey).Scan(&failures, &blockedUntil)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return 0, time.Time{}, nil
		}
		return 0, time.Time{}, fmt.Errorf("load password failures: %w", err)
	}
	if blockedUntil == nil {
		return failures, time.Time{}, nil
	}
	return failures, *blockedUntil, nil
}

func (s *cassandraAttemptsStore) Save(repoID, actorKey string, failures int, firstFailure, lastFailure, blockedUntil time.Time) error {
	var blockedUntilValue interface{}
	if !blockedUntil.IsZero() {
		blockedUntilValue = blockedUntil
	}
	return s.session.Query(`
		INSERT INTO encrypted_repo_password_failures
			(repo_id, actor_key, failure_count, first_failure, last_failure, blocked_until)
		VALUES (?, ?, ?, ?, ?, ?) USING TTL ?
	`, repoID, actorKey, failures, firstFailure, lastFailure, blockedUntilValue, int(passwordAttemptsTTL.Seconds())).Exec()
}

func (s *cassandraAttemptsStore) RecordFailure(repoID, actorKey string, now time.Time) (int, time.Time, error) {
	for attempt := 0; attempt < maxCASRetries; attempt++ {
		blockedUntil := blockedUntilForFailures(1, now)
		var blockedUntilValue interface{}
		if !blockedUntil.IsZero() {
			blockedUntilValue = blockedUntil
		}
		applied, err := s.session.Query(`
		INSERT INTO encrypted_repo_password_failures
			(repo_id, actor_key, failure_count, first_failure, last_failure, blocked_until)
		VALUES (?, ?, ?, ?, ?, ?) IF NOT EXISTS USING TTL ?
		`, repoID, actorKey, 1, now, now, blockedUntilValue, int(passwordAttemptsTTL.Seconds())).MapScanCAS(map[string]interface{}{})
		if err != nil {
			return 0, time.Time{}, fmt.Errorf("insert password failure row: %w", err)
		}
		if applied {
			return 1, blockedUntil, nil
		}

		currentFailures, _, err := s.Load(repoID, actorKey)
		if err != nil {
			return 0, time.Time{}, err
		}
		if currentFailures == 0 {
			continue
		}

		nextFailures := currentFailures + 1
		blockedUntil = blockedUntilForFailures(nextFailures, now)
		blockedUntilValue = nil
		if !blockedUntil.IsZero() {
			blockedUntilValue = blockedUntil
		}
		applied, err = s.session.Query(`
			UPDATE encrypted_repo_password_failures USING TTL ?
			SET failure_count = ?, last_failure = ?, blocked_until = ?
			WHERE repo_id = ? AND actor_key = ? IF failure_count = ?
		`, int(passwordAttemptsTTL.Seconds()), nextFailures, now, blockedUntilValue, repoID, actorKey, currentFailures).MapScanCAS(map[string]interface{}{})
		if err != nil {
			return 0, time.Time{}, fmt.Errorf("update password failure row: %w", err)
		}
		if applied {
			return nextFailures, blockedUntil, nil
		}
	}

	return 0, time.Time{}, fmt.Errorf("record password failure: contention too high after %d retries", maxCASRetries)
}

func (s *cassandraAttemptsStore) Reset(repoID, actorKey string) error {
	return s.session.Query(`
		DELETE FROM encrypted_repo_password_failures
		WHERE repo_id = ? AND actor_key = ?
	`, repoID, actorKey).Exec()
}

// ── In-memory store (tests) ───────────────────────────────────────────────

type memoryAttemptsStore struct {
	mu   sync.Mutex
	data map[string]memoryAttemptsEntry
}

type memoryAttemptsEntry struct {
	failures     int
	blockedUntil time.Time
}

func newMemoryAttemptsStore() *memoryAttemptsStore {
	return &memoryAttemptsStore{data: make(map[string]memoryAttemptsEntry)}
}

func (s *memoryAttemptsStore) key(repoID, actorKey string) string {
	return repoID + "|" + actorKey
}

func (s *memoryAttemptsStore) Load(repoID, actorKey string) (int, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[s.key(repoID, actorKey)]
	if !ok {
		return 0, time.Time{}, nil
	}
	return e.failures, e.blockedUntil, nil
}

func (s *memoryAttemptsStore) Save(repoID, actorKey string, failures int, _, _, blockedUntil time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[s.key(repoID, actorKey)] = memoryAttemptsEntry{failures: failures, blockedUntil: blockedUntil}
	return nil
}

func (s *memoryAttemptsStore) RecordFailure(repoID, actorKey string, now time.Time) (int, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.key(repoID, actorKey)
	entry := s.data[key]
	entry.failures++
	entry.blockedUntil = blockedUntilForFailures(entry.failures, now)
	s.data[key] = entry
	return entry.failures, entry.blockedUntil, nil
}

func (s *memoryAttemptsStore) Reset(repoID, actorKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, s.key(repoID, actorKey))
	return nil
}
