-- Migration 003: upload_staging
--
-- Durable staging records for chunked/browser uploads. These rows make the
-- temporary pre-publication state explicit so finalize retries and future
-- cleanup/recovery logic can distinguish uploads that are receiving bytes,
-- currently promoting, already published/closed, or pending cleanup.

CREATE TABLE IF NOT EXISTS upload_sessions (
    org_id          TEXT,
    upload_id       TEXT,
    repo_id         TEXT,
    user_id         TEXT,
    token_id        TEXT,
    parent_dir      TEXT,
    filename        TEXT,
    actual_filename TEXT,
    commit_id       TEXT,
    total_size      BIGINT,
    state           TEXT,
    created_at      TIMESTAMP,
    updated_at      TIMESTAMP,
    expires_at      TIMESTAMP,
    last_error      TEXT,
    PRIMARY KEY ((org_id), upload_id)
);

CREATE TABLE IF NOT EXISTS upload_session_blocks (
    upload_id      TEXT,
    block_index    INT,
    org_id         TEXT,
    block_sha1     TEXT,
    block_sha256   TEXT,
    size_bytes     INT,
    storage_class  TEXT,
    storage_key    TEXT,
    source         TEXT,
    state          TEXT,
    created_at     TIMESTAMP,
    updated_at     TIMESTAMP,
    uploaded_at    TIMESTAMP,
    promoted_at    TIMESTAMP,
    PRIMARY KEY ((upload_id), block_index)
);

CREATE TABLE IF NOT EXISTS upload_session_blocks_by_sha256 (
    org_id         TEXT,
    block_sha256   TEXT,
    upload_id      TEXT,
    block_index    INT,
    state          TEXT,
    updated_at     TIMESTAMP,
    PRIMARY KEY ((org_id, block_sha256), upload_id, block_index)
);

-- Phase-1 states include receiving, promoting, published, closed,
-- cleanup_pending, registered, uploaded, promoted and cleaned.