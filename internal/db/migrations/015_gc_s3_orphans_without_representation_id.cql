-- Migration 015: remove redundant block representation state from physical GC
--
-- R11a removed logical mapping cleanup from physical GC. The orphan's
-- representation_id no longer selects a physical target or authorizes an
-- action, and greenfield block creation already persists the canonical
-- representation outside this recovery row. Keep external_sha1 for the
-- remaining conservative canonical-state characterization.

ALTER TABLE gc_s3_orphans
    DROP IF EXISTS representation_id;
