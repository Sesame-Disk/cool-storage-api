-- API Keys: unified authentication for user API access, Accounts M2M, and Seafile legacy client compat.
-- Tokens are stored as SHA-256 hashes (raw token shown once on creation, never persisted).

CREATE TABLE IF NOT EXISTS api_keys (
    key_hash     text PRIMARY KEY,
    key_prefix   text,
    user_id      uuid,
    org_id       uuid,
    label        text,
    scope        text,
    created_at   timestamp,
    last_used_at timestamp,
    expires_at   timestamp
);

CREATE TABLE IF NOT EXISTS api_keys_by_user (
    org_id       uuid,
    user_id      uuid,
    key_hash     text,
    key_prefix   text,
    label        text,
    scope        text,
    created_at   timestamp,
    last_used_at timestamp,
    expires_at   timestamp,
    PRIMARY KEY ((org_id, user_id), created_at)
) WITH CLUSTERING ORDER BY (created_at DESC);
