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
-- the PRIMARY KEY ((first_seen_day, bucket), first_seen_at, org_id, block_id), so
-- a row carries no regular cells and its primary-key liveness IS the row. Only
-- INSERT writes that liveness. An UPDATE of this table is not merely discouraged,
-- it is inexpressible, because CQL requires a SET over a non-key column and none
-- remains -- so the hazard is conditional: re-add any regular column and an
-- UPDATE-based writer becomes expressible again.
--
-- What that would break is worth stating exactly, because the intuitive answer is
-- wrong. An UPDATE-created row is NOT invisible: Cassandra considers a row present
-- when it has live cells even with no PK liveness, which is why UPDATE upserts, so
-- the day scan would enumerate it normally at first. The defect is deferred — the
-- row's lifetime becomes its payload cell's, so it disappears when that cell is
-- deleted or expires under the 90-day TTL, dropping an identity that was still
-- supposed to be recoverable. Publication must stay an INSERT so a discovery row's
-- existence is carried by the identity itself, never by payload.
--
-- TestR22bProjectionWriteIsInsert pins the pair: INSERT, and exactly these five
-- columns. TestGC_R22bProjectionRowIsIdentityOnly confirms against the real engine
-- that such a row reads back, enumerates, and keeps the table's default TTL.
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
