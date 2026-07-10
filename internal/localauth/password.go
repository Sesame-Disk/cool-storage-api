// Package localauth implements optional local (username/password)
// authentication for SesameFS. It is deliberately self-contained and depends
// only on a gocql session plus a small policy config, so it can be shared by
// both the storage service (admin password management) and the standalone
// sesameauth login service without pulling in the HTTP layer.
//
// Credentials live in their own table (user_credentials) — never in the
// widely-read users aggregate or its admin projections. See migration 009.
package localauth

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// AlgoBcrypt is the identifier stored in user_credentials.password_algo.
const AlgoBcrypt = "bcrypt"

// bcryptCost balances login latency against brute-force resistance. The default
// (10) is ~50ms/verify; combined with the lockout table this is ample for an
// admin-provisioned account model.
const bcryptCost = bcrypt.DefaultCost

// HashPassword returns a bcrypt hash of the given plaintext password.
func HashPassword(plain string) (string, error) {
	// bcrypt silently truncates input beyond 72 bytes, which would make a long
	// password's tail meaningless. Reject rather than hash a truncated secret.
	if len(plain) > 72 {
		return "", fmt.Errorf("password exceeds 72 bytes")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(h), nil
}

// VerifyPassword reports whether plain matches the stored bcrypt hash.
func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// ValidatePassword enforces the configured password policy. It returns a
// user-facing error describing the first violation, or nil if acceptable.
func ValidatePassword(plain string, minLength int) error {
	if minLength < 1 {
		minLength = 1
	}
	if utf8.RuneCountInString(plain) < minLength {
		return fmt.Errorf("password must be at least %d characters", minLength)
	}
	if len(plain) > 72 {
		return fmt.Errorf("password must be at most 72 bytes")
	}
	if strings.TrimSpace(plain) == "" {
		return fmt.Errorf("password must not be blank")
	}
	return nil
}
