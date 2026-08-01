package localauth

import (
	"errors"
	"fmt"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// Sentinel errors returned by Authenticate. Callers should treat
// ErrInvalidCredentials uniformly (unknown email vs. wrong password) to avoid
// leaking which accounts exist.
var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrLockedOut          = errors.New("too many failed attempts; try again later")
	ErrAccountInactive    = errors.New("account is not active")
)

// Policy holds the runtime password/lockout policy.
type Policy struct {
	MinPasswordLength int
	MaxFailedAttempts int
	LockoutDuration   time.Duration
}

// Service provides high-level local-auth operations over a gocql session.
// It is safe for concurrent use.
type Service struct {
	session *gocql.Session
	policy  Policy
}

// NewService builds a Service. minPasswordLength <1 is normalized to 1.
func NewService(session *gocql.Session, policy Policy) *Service {
	if policy.MinPasswordLength < 1 {
		policy.MinPasswordLength = 8
	}
	return &Service{session: session, policy: policy}
}

// Identity is the result of a successful authentication — everything the caller
// needs to mint a session.
type Identity struct {
	UserID     string
	OrgID      string
	Email      string
	Name       string
	Role       string
	MustChange bool
}

// Authenticate verifies an email/password against the credential store, applies
// lockout, and confirms the backing user is active. ip scopes the lockout key.
func (s *Service) Authenticate(email, password, ip string, now time.Time) (*Identity, error) {
	email = NormalizeEmail(email)

	status, err := CheckLockout(s.session, email, ip, now)
	if err != nil {
		return nil, err
	}
	if status.Blocked {
		return nil, ErrLockedOut
	}

	cred, err := GetCredential(s.session, email)
	if err != nil {
		if errors.Is(err, ErrNoCredential) {
			// Record a failure even for unknown accounts so an attacker can't
			// enumerate emails by timing/lockout behavior.
			_ = RecordFailure(s.session, email, ip, s.policy.MaxFailedAttempts, s.policy.LockoutDuration, now)
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}

	if !VerifyPassword(cred.PasswordHash, password) {
		_ = RecordFailure(s.session, email, ip, s.policy.MaxFailedAttempts, s.policy.LockoutDuration, now)
		return nil, ErrInvalidCredentials
	}

	// Password correct — confirm the backing user is active before issuing a
	// session. Reads the canonical users row (read-only coupling by design).
	name, role, userStatus, err := s.readUser(cred.OrgID, cred.UserID)
	if err != nil {
		return nil, err
	}
	if userStatus != "" && userStatus != "active" {
		return nil, ErrAccountInactive
	}

	// Success — clear the failure counter.
	_ = ClearFailures(s.session, email, ip)

	return &Identity{
		UserID:     cred.UserID,
		OrgID:      cred.OrgID,
		Email:      email,
		Name:       name,
		Role:       role,
		MustChange: cred.MustChange,
	}, nil
}

// SetPassword validates and stores a password for an existing user. Used by the
// admin "set/reset password" path. mustChange forces a change on next login.
func (s *Service) SetPassword(email, userID, orgID, plain string, mustChange bool, now time.Time) error {
	if err := ValidatePassword(plain, s.policy.MinPasswordLength); err != nil {
		return err
	}
	hash, err := HashPassword(plain)
	if err != nil {
		return err
	}
	return SetCredential(s.session, email, userID, orgID, hash, mustChange, now)
}

// ChangePassword verifies the current password and stores a new one, clearing
// the must-change flag. Used by the authenticated self-service endpoint.
func (s *Service) ChangePassword(email, currentPassword, newPassword, ip string, now time.Time) error {
	email = NormalizeEmail(email)
	cred, err := GetCredential(s.session, email)
	if err != nil {
		if errors.Is(err, ErrNoCredential) {
			return ErrInvalidCredentials
		}
		return err
	}
	if !VerifyPassword(cred.PasswordHash, currentPassword) {
		return ErrInvalidCredentials
	}
	if err := ValidatePassword(newPassword, s.policy.MinPasswordLength); err != nil {
		return err
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	return SetCredential(s.session, email, cred.UserID, cred.OrgID, hash, false, now)
}

// Policy returns the effective policy (used by handlers for messaging).
func (s *Service) Policy() Policy { return s.policy }

func (s *Service) readUser(orgID, userID string) (name, role, status string, err error) {
	err = s.session.Query(`
		SELECT name, role, status FROM users WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Scan(&name, &role, &status)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			// Credential exists but the user was removed — treat as invalid.
			return "", "", "", ErrInvalidCredentials
		}
		return "", "", "", fmt.Errorf("read user: %w", err)
	}
	return name, role, status, nil
}
