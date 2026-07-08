-- Migration 009: representation-aware block identity plumbing
--
-- The old block_id_mappings table keyed only by (org_id, external_id). That is
-- insufficient once the same client-visible SHA-1 can legitimately name
-- different stored bytes in different library block representations.
--
-- ADD IF NOT EXISTS keeps this migration re-runnable: the migrator only stamps a
-- migration after ALL its statements succeed, so a transient failure on a later
-- statement would otherwise leave the already-applied ALTERs to fail with
-- "column already exists" on the next boot and wedge startup. Cassandra 5.0
-- supports IF NOT EXISTS on ALTER ... ADD.

ALTER TABLE libraries
	ADD IF NOT EXISTS block_representation_id TEXT;

ALTER TABLE libraries_by_id
	ADD IF NOT EXISTS block_representation_id TEXT;

ALTER TABLE blocks
	ADD IF NOT EXISTS representation_id TEXT;

ALTER TABLE gc_s3_orphans
	ADD IF NOT EXISTS representation_id TEXT;

ALTER TABLE gc_s3_orphans_by_day
	ADD IF NOT EXISTS representation_id TEXT;


DROP TABLE IF EXISTS block_id_mappings;

CREATE TABLE IF NOT EXISTS block_id_mappings (
	org_id            UUID,
	representation_id TEXT,
	external_id       TEXT,
	internal_id       TEXT,
	created_at        TIMESTAMP,
	PRIMARY KEY ((org_id, representation_id, external_id))
);