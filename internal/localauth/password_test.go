package localauth

import (
	"strings"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	const pw = "correct horse battery staple"
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	if hash == pw {
		t.Fatal("hash must not equal plaintext")
	}
	if !VerifyPassword(hash, pw) {
		t.Fatal("VerifyPassword should accept the correct password")
	}
	if VerifyPassword(hash, "wrong password") {
		t.Fatal("VerifyPassword should reject an incorrect password")
	}
}

func TestHashPasswordIsSalted(t *testing.T) {
	h1, _ := HashPassword("same-password")
	h2, _ := HashPassword("same-password")
	if h1 == h2 {
		t.Fatal("two hashes of the same password must differ (per-hash salt)")
	}
}

func TestHashPasswordRejectsOver72Bytes(t *testing.T) {
	long := strings.Repeat("a", 73)
	if _, err := HashPassword(long); err == nil {
		t.Fatal("expected error for >72-byte password (bcrypt truncation guard)")
	}
}

func TestVerifyPasswordRejectsGarbageHash(t *testing.T) {
	if VerifyPassword("not-a-bcrypt-hash", "whatever") {
		t.Fatal("VerifyPassword must not accept against a malformed hash")
	}
}

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name      string
		password  string
		minLength int
		wantErr   bool
	}{
		{"meets minimum", "12345678", 8, false},
		{"below minimum", "1234567", 8, true},
		{"blank", "        ", 8, true},
		{"empty", "", 8, true},
		{"unicode counts runes not bytes", "héllo👍", 6, false},
		{"over 72 bytes", strings.Repeat("a", 73), 8, true},
		{"minLength normalized when zero", "a", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePassword(tc.password, tc.minLength)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestNormalizeEmail(t *testing.T) {
	cases := map[string]string{
		"  Foo@Example.COM ": "foo@example.com",
		"already@lower.com":  "already@lower.com",
		"\tSpace@X.io\n":     "space@x.io",
	}
	for in, want := range cases {
		if got := NormalizeEmail(in); got != want {
			t.Errorf("NormalizeEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestActorKeyIsStableAndNormalized(t *testing.T) {
	a := actorKey("USER@Example.com", "10.0.0.1")
	b := actorKey(" user@example.com ", "10.0.0.1")
	if a != b {
		t.Fatalf("actorKey should normalize email: %q != %q", a, b)
	}
	if !strings.Contains(a, "|10.0.0.1") {
		t.Fatalf("actorKey should include the ip segment, got %q", a)
	}
	// Different IPs must not share a lockout bucket.
	if actorKey("u@x.io", "1.1.1.1") == actorKey("u@x.io", "2.2.2.2") {
		t.Fatal("different IPs must produce different actor keys")
	}
}
