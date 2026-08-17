-- Migration 014: make gc_s3_orphans_by_day physically identity-only
--
-- R22a removed recovery authority from the discovery projection at the API
-- level: ListS3OrphansByDay selects only (first_seen_at, org_id, block_id) and
-- S3OrphanDiscoveryInfo carries nothing else, so reaching for projection payload
-- in a recovery decision became a compile error. The columns themselves stayed,
-- still written by upsertS3OrphanProjection with zero readers.
--
-- Write-only duplicates of canonical state are not free: they can diverge from
-- gc_s3_orphans, they keep refreshing TTL cells that serve no purpose, and they
-- leave a payload sitting in the projection for a future refactor to start
-- trusting again. R22b drops them, which turns R22a's API separation into a
-- storage separation — "the worker does not read the payload" becomes "the
-- payload does not exist".
--
-- storage_class came from 001_initial_schema.cql; external_sha1 and
-- recovery_phase from 007_gc_s3_orphan_mapping_recovery.cql; representation_id
-- from 009_block_representation_mappings.cql.
--
-- After this migration every remaining column of gc_s3_orphans_by_day is part of
-- the PRIMARY KEY ((first_seen_day, bucket), first_seen_at, org_id, block_id),
-- so a row has no regular cells and exists solely by virtue of its row marker,
-- which only INSERT writes. Note what this does NOT mean: an UPDATE of this table
-- is not merely discouraged, it is inexpressible, because CQL requires a SET over
-- a non-key column and none remains. The hazard is conditional — re-add any
-- regular column and an UPDATE-based writer becomes expressible again, and would
-- then create rows carrying that column but no row marker, which the day scan
-- would not enumerate. TestR22bProjectionWriteIsInsert therefore pins the pair:
-- INSERT, and exactly these five columns. TestGC_R22bProjectionRowIsIdentityOnly
-- confirms against the real engine that a marker-only row reads back and that the
-- table keeps its default TTL.
--
-- Note on canonical: gc_s3_orphans keeps all four columns. They are the
-- authority recovery reads; only the projection copy is removed.

ALTER TABLE gc_s3_orphans_by_day
	DROP IF EXISTS storage_class;

ALTER TABLE gc_s3_orphans_by_day
	DROP IF EXISTS representation_id;

ALTER TABLE gc_s3_orphans_by_day
	DROP IF EXISTS external_sha1;

ALTER TABLE gc_s3_orphans_by_day
	DROP IF EXISTS recovery_phase;
