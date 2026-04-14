-- Migration 003: encrypted_repo_password_failures
--
-- Online brute-force protection for encrypted libraries. PBKDF2 at 1000
-- iterations (Seafile compat) is fast enough that an authenticated attacker
-- could try thousands of passwords per second against /set-password; even the
-- Argon2id strong path is only slow per-attempt (~200ms) and tempting to
-- bulk-probe. This table records failed attempts per (repo_id, key) so the
-- application can block further attempts after a threshold and enforce
-- exponential backoff across process restarts.
--
-- Rows are keyed by actor (user_id|ip) to avoid one abusive client denying
-- service to the rest of the org. TTL of 24h bounds table growth and also
-- caps the worst-case block duration naturally.

CREATE TABLE IF NOT EXISTS encrypted_repo_password_failures (
    repo_id        TEXT,
    actor_key      TEXT,
    failure_count  INT,
    first_failure  TIMESTAMP,
    last_failure   TIMESTAMP,
    blocked_until  TIMESTAMP,
    PRIMARY KEY ((repo_id), actor_key)
) WITH default_time_to_live = 86400;
