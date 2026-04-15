# Encryption — Preproduction Assessment

**Date:** 2026-04-14
**Diagrams:** [Encryption Flow Diagrams](./diagrams/encryption-flow.md)

---

## Issues found

### HIGH: No end-to-end encrypted file test

There is no integration test that creates an encrypted library, sets a password,
uploads a file, downloads it, and verifies the content matches. The encrypt/decrypt
code paths are only tested in unit tests with synthetic data.

**Risk:** A regression in key derivation, block encryption, or the decrypt session
could silently corrupt every file in encrypted libraries. This would not be detected
until a customer reports it.

**Fix:** Add integration test that exercises the full pipeline with real Cassandra + S3.

---

### HIGH: PBKDF2 at 1000 iterations (Seafile compat)

**File:** `internal/crypto/crypto.go:62`

The Seafile-compatible encryption path uses PBKDF2-HMAC-SHA256 with only 1,000
iterations. OWASP 2024 recommends 600,000+. If an attacker compromises Cassandra
(which stores the `magic` password verifier and encrypted `random_key`), they can
brute-force weak passwords offline at high speed.

**Mitigated by:** The Argon2id strong path (64MB, 3 iterations) is used for web/API
clients. But any library unlocked by a Seafile desktop/mobile client is also unlockable
via the weak PBKDF2 path.

**Fix (short-term):** Enforce minimum password complexity for encrypted libraries.
**Fix (long-term):** Deprecate PBKDF2 compat or raise to 600k+ iterations (breaks Seafile clients).

---

### MEDIUM: No password attempt rate limiting

**File:** `internal/api/v2/` (set-password endpoint)

There is no per-library or per-user rate limit on wrong password attempts for
encrypted libraries. The only protection is the global per-IP rate limiter on
auth endpoints, which does not cover the set-password endpoint specifically.

**Risk:** Online brute-force against weak passwords. Combined with the PBKDF2 issue,
a determined attacker could try thousands of passwords per second.

**Fix:** Add per-library rate limiting on password attempts (e.g., 5 attempts per
minute, exponential backoff after 10 failures).

**Tested:** No. No test verifies rate limiting on wrong password attempts.

---

### MEDIUM: Decrypt session not invalidated on password change

When a library password is changed, existing decrypt sessions for other users
remain valid with the old file key. The file key itself doesn't change (only its
encryption wrapping), so the old sessions still work — but this is not explicitly
tested or documented as intentional behavior.

**Risk:** If an admin changes a library password to revoke access, users with
active sessions continue to have access until the 1-hour TTL expires.

**Tested:** No.

---

### MEDIUM: Seafile block format uses static IV per library

**File:** `internal/crypto/crypto.go:429` (EncryptBlockSeafile)

The Seafile-compatible path derives the CBC IV from the password (static per library).
All blocks in the same library are encrypted with the same IV. This is a known
weakness of CBC mode — identical first blocks of different files produce identical
ciphertext, leaking information.

**Mitigated by:** The SesameFS format (EncryptBlock) uses random IV per block.
But any block uploaded via Seafile sync uses the weak format.

**Fix:** Can't change without breaking Seafile client compatibility. Document as
known limitation. Prefer web/API uploads for sensitive data.

---

### LOW: Key material lifetime in memory

**File:** `internal/api/v2/encryption.go` (DecryptSessionManager)

File keys stay in the Go process memory for the full 1-hour session TTL. When the
session expires, the reference is removed from the map, but Go's garbage collector
determines when the actual memory is freed. There is no explicit zeroing of key bytes.

**Risk:** If the process memory is dumped (core dump, swap to disk), key material
may be recoverable. Low risk in practice.

**Fix:** Use a `[]byte` wrapper that zeros on finalization, or reduce session TTL.

---

### LOW: Metadata not encrypted

File names, directory structure, file sizes, modification times, share links, and
commit messages are stored in plaintext in Cassandra. Only block content is encrypted.

**Risk:** An attacker who compromises Cassandra can see what files exist, their names
and sizes, and who shared what — even for encrypted libraries.

**Fix:** This is a fundamental design choice inherited from Seafile and not easily
changed. Document clearly for users who expect full encryption.

---

## Test coverage

### Existing tests: 50+

| What's tested | Count | Type |
|---------------|-------|------|
| Key derivation (PBKDF2 + Argon2id) | 3 | Unit |
| Magic computation + verification | 3 | Unit |
| File key encrypt/decrypt round-trip | 1 | Unit |
| Library creation with encryption | 1 | Unit |
| Password change (happy + wrong old) | 2 | Unit |
| Block encrypt/decrypt Seafile format | 1 | Unit |
| Block encrypt/decrypt SesameFS format | 1 | Unit |
| Input validation (bad key/IV/size) | 12 | Unit |
| Legacy unencrypted block fallback | 3 | Unit |
| All enc_version paths (v2, v4, dual) | 5 | Unit |
| Decrypt session lifecycle (unlock/lock/expiry) | 5 | Unit |
| Decrypt session concurrency | 1 | Unit |
| Seafile compatibility (magic format) | 9 | Unit |
| Full encrypt→decrypt flow | 1 | Unit |
| Encrypted library creation via API | 1 | Integration |
| KDF performance benchmarks | 2 | Benchmark |

### Critical gaps: 6

| Gap | Risk | What to add |
|-----|------|-------------|
| Encrypted file upload + download round-trip | High | Create enc lib → unlock → upload → download → verify SHA-256 |
| Wrong password brute-force rate | Medium | Send 100 wrong passwords → verify rate limiting or lockout |
| Password change + active sessions | Medium | Change password → verify old sessions still/don't work |
| Decrypt session expiry during download | Medium | Start large download → expire session → verify completion |
| Seafile client encrypted block via sync | Medium | Upload encrypted block via PUT /seafhttp/block → download → verify |
| OnlyOffice encrypted document save | Medium | Save to encrypted lib via OnlyOffice callback → verify |

### How to run

```bash
# All crypto unit tests
go test -v ./internal/crypto/...

# Encryption handler tests
go test -v -run "TestSetPassword\|TestChangePassword\|TestDecryptSession\|TestEncryption" \
    ./internal/api/v2/...

# Integration
go test -tags integration -v -run "TestEncryptedLibrary" ./internal/integration/...

# KDF benchmarks
go test -bench=. ./internal/crypto/...
```

---

## Best practices check

| Practice | Status |
|----------|--------|
| Password verified with constant-time comparison | Yes (`subtle.ConstantTimeCompare`) |
| File key never stored in plaintext in DB | Yes (encrypted with password-derived key) |
| Per-library random salt | Yes (32 bytes, `crypto/rand`) |
| Strong KDF available | Yes (Argon2id, OWASP-recommended params) |
| Weak KDF documented | Yes (comments explain PBKDF2 is for Seafile compat) |
| Key material in logs | No (verified — no keys/passwords/magic logged) |
| Password change doesn't require re-encryption | Yes (only re-wraps file key — correct design) |
| Wrong password returns generic error | Yes ("Wrong password", no info leak) |
| Rate limiting on password attempts | **No** |
| Key material zeroed after use | **No** (relies on Go GC) |
| Metadata (filenames) encrypted | **No** (by design, but should be documented for users) |
