-- Migration 009: local authentication (username/password) support.
--
-- SesameFS historically authenticated only via OIDC (real sessions) or static
-- dev tokens (config-only, no login). This migration adds first-class local
-- accounts: an admin creates a user, sets a password, and the user logs in
-- through the optional sesameauth service, which mints a normal session.
--
-- DESIGN: credentials are deliberately kept OUT of the `users` table and its
-- denormalized admin projections. A password is a single-access-pattern secret
-- (email -> hash + identity -> verify -> mint session); it is never a query or
-- sort dimension for any admin listing. Isolating it here means:
--   * zero changes to the widely-SELECTed `users` aggregate,
--   * zero fan-out to users_by_email / users_admin_global_* / projections,
--   * the optional auth service owns exactly these two tables.

-- Credential store. Keyed by email — the same unique login identifier already
-- indexed by users_by_email — so the auth service can resolve a credential in a
-- single partition read. user_id/org_id are cached here to avoid a second
-- lookup on the hot login path; they are immutable for the life of the account.
CREATE TABLE IF NOT EXISTS user_credentials (
    email          TEXT PRIMARY KEY,
    user_id        UUID,
    org_id         UUID,
    password_hash  TEXT,     -- opaque; format determined by password_algo
    password_algo  TEXT,     -- e.g. "bcrypt"
    must_change    BOOLEAN,  -- force a change-password on next login (temp/admin-set passwords)
    created_at     TIMESTAMP,
    updated_at     TIMESTAMP
);

-- Online brute-force protection for local login, mirroring the pattern used by
-- encrypted_repo_password_failures (migration 002). Rows are keyed by actor
-- (email|ip) so one abusive client cannot lock out an entire account globally,
-- and a 24h TTL bounds table growth and caps worst-case block duration.
CREATE TABLE IF NOT EXISTS local_login_failures (
    actor_key      TEXT PRIMARY KEY,   -- "<email>|<ip>"
    failure_count  INT,
    first_failure  TIMESTAMP,
    last_failure   TIMESTAMP,
    blocked_until  TIMESTAMP
) WITH default_time_to_live = 86400;
