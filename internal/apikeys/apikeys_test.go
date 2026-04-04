package apikeys

import (
	"strings"
	"testing"
	"time"
)

func TestScopeAllows(t *testing.T) {
	tests := []struct {
		keyScope string
		required string
		want     bool
	}{
		// read key
		{ScopeRead, ScopeRead, true},
		{ScopeRead, ScopeReadWrite, false},
		{ScopeRead, ScopeAdmin, false},
		// read-write key
		{ScopeReadWrite, ScopeRead, true},
		{ScopeReadWrite, ScopeReadWrite, true},
		{ScopeReadWrite, ScopeAdmin, false},
		// admin key
		{ScopeAdmin, ScopeRead, true},
		{ScopeAdmin, ScopeReadWrite, true},
		{ScopeAdmin, ScopeAdmin, true},
		// unknown required scope
		{ScopeAdmin, "unknown", false},
		{ScopeRead, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.keyScope+"→"+tt.required, func(t *testing.T) {
			got := ScopeAllows(tt.keyScope, tt.required)
			if got != tt.want {
				t.Errorf("ScopeAllows(%q, %q) = %v, want %v", tt.keyScope, tt.required, got, tt.want)
			}
		})
	}
}

func TestConstrainRoleForScope(t *testing.T) {
	tests := []struct {
		name  string
		role  string
		scope string
		want  string
	}{
		{name: "read caps admin to readonly", role: "admin", scope: ScopeRead, want: "readonly"},
		{name: "read write caps owner to user", role: "owner", scope: ScopeReadWrite, want: "user"},
		{name: "read write keeps readonly readonly", role: "readonly", scope: ScopeReadWrite, want: "readonly"},
		{name: "admin keeps superadmin", role: "superadmin", scope: ScopeAdmin, want: "superadmin"},
		{name: "unknown scope leaves role unchanged", role: "user", scope: "unknown", want: "user"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConstrainRoleForScope(tt.role, tt.scope); got != tt.want {
				t.Fatalf("ConstrainRoleForScope(%q, %q) = %q, want %q", tt.role, tt.scope, got, tt.want)
			}
		})
	}
}

func TestIsValidScope(t *testing.T) {
	for _, s := range []string{ScopeRead, ScopeReadWrite, ScopeAdmin} {
		if !IsValidScope(s) {
			t.Errorf("IsValidScope(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "write", "ADMIN", "superadmin"} {
		if IsValidScope(s) {
			t.Errorf("IsValidScope(%q) = true, want false", s)
		}
	}
}

func TestHashTokenDeterministic(t *testing.T) {
	token := "sk_abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	h1 := HashToken(token)
	h2 := HashToken(token)
	if h1 != h2 {
		t.Errorf("HashToken not deterministic: %s != %s", h1, h2)
	}
	if len(h1) != 64 { // SHA-256 = 32 bytes = 64 hex chars
		t.Errorf("HashToken length = %d, want 64", len(h1))
	}
}

func TestHashTokenDifferentInputs(t *testing.T) {
	h1 := HashToken("sk_aaaa")
	h2 := HashToken("sk_bbbb")
	if h1 == h2 {
		t.Error("HashToken should produce different hashes for different inputs")
	}
}

func TestTokenFormat(t *testing.T) {
	// Simulate what CreateKey does for the token format.
	rawBytes := make([]byte, tokenBytes)
	for i := range rawBytes {
		rawBytes[i] = byte(i)
	}
	rawToken := tokenPrefix + strings.Repeat("ab", tokenBytes)

	if !strings.HasPrefix(rawToken, "sk_") {
		t.Errorf("token should start with sk_, got %s", rawToken[:10])
	}
	expectedLen := len(tokenPrefix) + tokenBytes*2 // "sk_" + hex
	if len(rawToken) != expectedLen {
		t.Errorf("token length = %d, want %d", len(rawToken), expectedLen)
	}
}

func TestCheckExpiry(t *testing.T) {
	// nil expiry = no expiration
	key := &APIKey{ExpiresAt: nil}
	if err := checkExpiry(key); err != nil {
		t.Errorf("nil expiry should not error, got %v", err)
	}

	// future expiry = valid
	future := time.Now().UTC().Add(24 * time.Hour)
	key.ExpiresAt = &future
	if err := checkExpiry(key); err != nil {
		t.Errorf("future expiry should not error, got %v", err)
	}

	// past expiry = expired
	past := time.Now().UTC().Add(-1 * time.Hour)
	key.ExpiresAt = &past
	if err := checkExpiry(key); err != ErrKeyExpired {
		t.Errorf("past expiry should return ErrKeyExpired, got %v", err)
	}
}

func TestTTLFromExpiry(t *testing.T) {
	now := time.Now().UTC()

	t.Run("nil expiry disables ttl", func(t *testing.T) {
		ttl, err := ttlFromExpiry(now, nil)
		if err != nil {
			t.Fatalf("ttlFromExpiry(nil) error = %v", err)
		}
		if ttl != 0 {
			t.Fatalf("ttlFromExpiry(nil) = %d, want 0", ttl)
		}
	})

	t.Run("future expiry yields positive ttl", func(t *testing.T) {
		expiresAt := now.Add(2 * time.Hour)
		ttl, err := ttlFromExpiry(now, &expiresAt)
		if err != nil {
			t.Fatalf("ttlFromExpiry(future) error = %v", err)
		}
		if ttl <= 0 {
			t.Fatalf("ttlFromExpiry(future) = %d, want > 0", ttl)
		}
	})

	t.Run("past expiry is rejected", func(t *testing.T) {
		expiresAt := now.Add(-1 * time.Minute)
		_, err := ttlFromExpiry(now, &expiresAt)
		if err != ErrInvalidExpiry {
			t.Fatalf("ttlFromExpiry(past) error = %v, want %v", err, ErrInvalidExpiry)
		}
	})
}

func TestKeyPrefixFormat(t *testing.T) {
	// Simulate prefix generation.
	rawToken := "sk_a3f8b2c1d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0"
	prefix := rawToken[:len(tokenPrefix)+prefixLen] + "..."
	if prefix != "sk_a3f8b2c1..." {
		t.Errorf("prefix = %q, want %q", prefix, "sk_a3f8b2c1...")
	}
	if len(prefix) != len("sk_")+prefixLen+len("...") {
		t.Errorf("prefix length = %d, want %d", len(prefix), len("sk_")+prefixLen+len("..."))
	}
}
