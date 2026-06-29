-- Migration 005: additive schema for SHA-256-canonical fs_object block IDs
--
-- Adds:
--   - fs_objects.seafile_block_ids_sha1: the SHA-1 block-id list used at the
--     Seafile compatibility boundary (fs_object serialization + fs_id).
--   - blocks.sha1: authoritative SHA-256 -> SHA-1 lookup sourced from the real
--     block bytes the server already hashes on upload/finalize paths.
--
-- This migration is additive only: existing readers/writers continue using the
-- current schema until later PRs flip behavior.

ALTER TABLE fs_objects ADD seafile_block_ids_sha1 LIST<TEXT>;
ALTER TABLE blocks ADD sha1 TEXT;
