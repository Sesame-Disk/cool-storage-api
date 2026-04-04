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

ALTER TABLE sessions ADD source_api_key_hash text;
ALTER TABLE sessions ADD api_key_scope text;

CREATE TABLE IF NOT EXISTS sessions_by_api_key (
    api_key_hash text,
    token_hash   text,
    org_id       uuid,
    user_id      uuid,
    PRIMARY KEY ((api_key_hash), token_hash)
);
