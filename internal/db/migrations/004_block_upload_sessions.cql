-- Migration 004: block_upload_sessions
--
-- Server-issued sessions for the web content-addressed (block) upload flow.
-- See docs/WEB-BLOCK-UPLOAD.md.
--
-- A session ties a batch of /blocks/upload calls and the final file-from-blocks
-- commit to one (org, user, repo), bounds them with a TTL, and records the
-- committed result so a retried commit is idempotent instead of creating a
-- duplicate auto-renamed file. `session_id` is a random 256-bit token that also
-- serves as the provisional block reference owner ("up:<session_id>"), so a
-- block uploaded under a session is only publishable by that same session.
--
-- Rows carry a TTL (aligned with the provisional block reference TTL) so
-- abandoned sessions self-expire. org_id/user_id/repo_id are UUID to match the
-- rest of the schema (blocks, libraries, …); the API layer passes them as UUID
-- strings, which gocql marshals/unmarshals to the UUID column transparently
-- (same pattern as block_references).

CREATE TABLE IF NOT EXISTS block_upload_sessions (
    session_id       TEXT,
    org_id           UUID,
    user_id          UUID,
    repo_id          UUID,
    parent_dir       TEXT,
    created_at       TIMESTAMP,
    expires_at       TIMESTAMP,
    committed        BOOLEAN,
    manifest_digest  TEXT,
    result_path      TEXT,
    result_filename  TEXT,
    result_commit_id TEXT,
    PRIMARY KEY (session_id)
);
