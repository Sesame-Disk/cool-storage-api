package localauth

import (
	"errors"
	"fmt"
	"strings"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// ErrNoCredential is returned when no credential row exists for an email.
var ErrNoCredential = errors.New("no credential for user")

// Credential is a stored local-auth credential.
type Credential struct {
	Email        string
	UserID       string
	OrgID        string
	PasswordHash string
	PasswordAlgo string
	MustChange   bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// NormalizeEmail lowercases and trims an email so lookups are case-insensitive.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// GetCredential loads the credential for an email. Returns ErrNoCredential if
// none exists.
func GetCredential(session *gocql.Session, email string) (*Credential, error) {
	cred := &Credential{Email: NormalizeEmail(email)}
	err := session.Query(`
		SELECT user_id, org_id, password_hash, password_algo, must_change, created_at, updated_at
		FROM user_credentials WHERE email = ?
	`, cred.Email).Scan(
		&cred.UserID, &cred.OrgID, &cred.PasswordHash, &cred.PasswordAlgo,
		&cred.MustChange, &cred.CreatedAt, &cred.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return nil, ErrNoCredential
		}
		return nil, fmt.Errorf("load credential: %w", err)
	}
	return cred, nil
}

// HasCredential reports whether a credential exists for the email.
func HasCredential(session *gocql.Session, email string) (bool, error) {
	_, err := GetCredential(session, email)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrNoCredential) {
		return false, nil
	}
	return false, err
}

// SetCredential upserts a credential row from an already-hashed password. The
// caller is responsible for hashing (via HashPassword) and for policy checks.
func SetCredential(session *gocql.Session, email, userID, orgID, passwordHash string, mustChange bool, now time.Time) error {
	email = NormalizeEmail(email)
	// Preserve created_at on updates; only set it when the row is new.
	var createdAt time.Time
	existing, err := GetCredential(session, email)
	switch {
	case err == nil:
		createdAt = existing.CreatedAt
	case errors.Is(err, ErrNoCredential):
		createdAt = now
	default:
		return err
	}
	if err := session.Query(`
		INSERT INTO user_credentials (email, user_id, org_id, password_hash, password_algo, must_change, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, email, userID, orgID, passwordHash, AlgoBcrypt, mustChange, createdAt, now).Exec(); err != nil {
		return fmt.Errorf("upsert credential: %w", err)
	}
	return nil
}

// DeleteCredential removes a credential row (used when a user is hard-deleted).
func DeleteCredential(session *gocql.Session, email string) error {
	return session.Query(`DELETE FROM user_credentials WHERE email = ?`, NormalizeEmail(email)).Exec()
}
