-- Migration 021: terminal library publication authority.
--
-- HEAD writers and hard-delete share the libraries row's SERIAL Paxos domain:
-- writers may advance HEAD only while publication_state is ACTIVE, and a
-- hard-delete first commits ACTIVE -> TERMINAL before removing the row.
--
-- Existing rows have NULL until they are touched by a writer or hard-delete.
-- Runtime CAS code treats NULL as legacy ACTIVE and materializes ACTIVE on its
-- first successful HEAD mutation/revocation, so this migration needs no table
-- scan or unsafe bulk rewrite.
--
-- The revocation table is a durable witness for the period after the canonical
-- libraries row is physically deleted. It is intentionally never deleted and
-- has no TTL: repair may use it only as evidence that the SERIAL terminal
-- transition completed before hard-delete proceeded.

ALTER TABLE libraries
    ADD IF NOT EXISTS publication_state TEXT;

CREATE TABLE IF NOT EXISTS library_publication_revocations (
    org_id     UUID,
    library_id UUID,
    revoked_at TIMESTAMP,
    PRIMARY KEY ((org_id), library_id)
);
