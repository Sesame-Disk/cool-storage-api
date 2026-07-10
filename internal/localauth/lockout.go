package localauth

import (
	"errors"
	"fmt"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// actorKey builds the lockout partition key from the login identifier and the
// client IP. Keying on both means one abusive IP cannot lock a victim's account
// across all clients, while still throttling a focused attack on one account.
func actorKey(email, ip string) string {
	return NormalizeEmail(email) + "|" + ip
}

// LockoutStatus describes whether an actor is currently blocked.
type LockoutStatus struct {
	Blocked bool
	Until   time.Time
}

// CheckLockout reports whether the (email, ip) actor is currently blocked.
func CheckLockout(session *gocql.Session, email, ip string, now time.Time) (LockoutStatus, error) {
	var blockedUntil time.Time
	err := session.Query(`
		SELECT blocked_until FROM local_login_failures WHERE actor_key = ?
	`, actorKey(email, ip)).Scan(&blockedUntil)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return LockoutStatus{}, nil
		}
		return LockoutStatus{}, fmt.Errorf("check lockout: %w", err)
	}
	if !blockedUntil.IsZero() && blockedUntil.After(now) {
		return LockoutStatus{Blocked: true, Until: blockedUntil}, nil
	}
	return LockoutStatus{}, nil
}

// RecordFailure increments the failure counter for an actor and sets a block
// window once maxAttempts is reached. maxAttempts <= 0 disables lockout.
func RecordFailure(session *gocql.Session, email, ip string, maxAttempts int, lockout time.Duration, now time.Time) error {
	if maxAttempts <= 0 {
		return nil
	}
	key := actorKey(email, ip)

	var count int
	var firstFailure time.Time
	err := session.Query(`
		SELECT failure_count, first_failure FROM local_login_failures WHERE actor_key = ?
	`, key).Scan(&count, &firstFailure)
	if err != nil && !errors.Is(err, gocql.ErrNotFound) {
		return fmt.Errorf("read failure count: %w", err)
	}
	if firstFailure.IsZero() {
		firstFailure = now
	}
	count++

	var blockedUntil time.Time
	if count >= maxAttempts {
		blockedUntil = now.Add(lockout)
	}

	if err := session.Query(`
		INSERT INTO local_login_failures (actor_key, failure_count, first_failure, last_failure, blocked_until)
		VALUES (?, ?, ?, ?, ?)
	`, key, count, firstFailure, now, blockedUntil).Exec(); err != nil {
		return fmt.Errorf("record failure: %w", err)
	}
	return nil
}

// ClearFailures removes the failure record for an actor after a successful login.
func ClearFailures(session *gocql.Session, email, ip string) error {
	return session.Query(`DELETE FROM local_login_failures WHERE actor_key = ?`, actorKey(email, ip)).Exec()
}
