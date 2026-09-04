-- Migration 021: terminal library publication authority.
--
-- HEAD writers and hard-delete share the libraries row's SERIAL Paxos domain:
-- writers may advance HEAD only while publication_state is ACTIVE, and a
-- hard-delete first commits ACTIVE -> TERMINAL before removing the row.
--
-- publication_state is mandatory application state, not an optional column
-- with a legacy fallback: every production INSERT INTO libraries sets it to
-- ACTIVE explicitly at creation. A row observed with any other value --
-- NULL, empty, or unrecognized -- is invalid and every caller (initial HEAD
-- writers, ordinary HEAD writers, restore, revoke, and the plain readers in
-- internal/db/library_state.go) fails closed on it rather than treating it
-- as ACTIVE. This codebase supports no pre-existing dataset of any kind: this
-- is a clean deploy from an empty keyspace, this migration ships as part of
-- the initial schema baseline before any real row is ever created, and this
-- policy holds until X1 closes and a doc explicitly says otherwise (see
-- CURRENT_WORK.md "Policy"). See docs/CHANGELOG.md "W2a pre-merge closure
-- round 3" for the fix that removed the legacy-NULL compatibility retry this
-- migration originally shipped alongside.
--
-- The revocation table is a durable witness for the period after the canonical
-- libraries row is physically deleted. It is intentionally never deleted and
-- has no TTL: repair may use it only as evidence that the SERIAL terminal
-- transition completed before hard-delete proceeded. Its partition key is the
-- full (org_id, library_id) pair, not org_id alone: every revoked library gets
-- its own small partition instead of every hard-delete an org ever performs
-- accumulating in one ever-growing per-org partition.
--
-- Deployment: this release must roll out atomically, the same as every other
-- migration in this pre-production codebase -- there is no existing writer
-- fleet to stage a mixed-version rollout against.

ALTER TABLE libraries
    ADD IF NOT EXISTS publication_state TEXT;

CREATE TABLE IF NOT EXISTS library_publication_revocations (
    org_id     UUID,
    library_id UUID,
    revoked_at TIMESTAMP,
    PRIMARY KEY ((org_id, library_id))
);
