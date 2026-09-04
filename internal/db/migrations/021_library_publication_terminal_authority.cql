-- Migration 021: terminal library publication authority.
--
-- HEAD writers and hard-delete share the libraries row's SERIAL Paxos domain:
-- writers may advance HEAD only while publication_state is ACTIVE, and a
-- hard-delete first commits ACTIVE -> TERMINAL before removing the row.
--
-- Existing rows have NULL until they are touched by a writer or hard-delete.
-- Runtime CAS code treats NULL as legacy ACTIVE and materializes ACTIVE on its
-- first successful HEAD mutation/revocation, so this migration needs no table
-- scan or unsafe bulk rewrite. That NULL is ambiguous by itself -- Cassandra
-- returns the same NULL for a genuine untouched legacy row and for a
-- partition that does not exist at all -- so any caller that would grant
-- ACTIVE authority on it must first check library_publication_revocations;
-- see db.LibraryPublicationCASStateIsLegacyNull and db.IsLibraryPublicationRevoked.
--
-- The revocation table is a durable witness for the period after the canonical
-- libraries row is physically deleted. It is intentionally never deleted and
-- has no TTL: repair may use it only as evidence that the SERIAL terminal
-- transition completed before hard-delete proceeded. Its partition key is the
-- full (org_id, library_id) pair, not org_id alone: every revoked library gets
-- its own small partition instead of every hard-delete an org ever performs
-- accumulating in one ever-growing per-org partition.
--
-- Deployment: this release must roll out atomically. A writer that predates
-- this migration CASes HEAD on `IF head_commit_id = ?` alone and does not
-- know about publication_state, so it can still advance HEAD after a
-- publication_state = TERMINAL hard-delete has committed. Do not run old and
-- new binaries concurrently against the same libraries rows once hard-delete
-- terminal authority is in use.

ALTER TABLE libraries
    ADD IF NOT EXISTS publication_state TEXT;

CREATE TABLE IF NOT EXISTS library_publication_revocations (
    org_id     UUID,
    library_id UUID,
    revoked_at TIMESTAMP,
    PRIMARY KEY ((org_id, library_id))
);
