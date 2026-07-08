-- Migration 010: persist block representation context across GC queue / DLQ
--
-- fs_object GC may execute after a library is soft-deleted or fully hard-deleted.
-- Once that happens, re-resolving SHA-1 block ids from live library state is no
-- longer reliable. Persist the block_representation_id with queued GC work and
-- with deleted_libraries so the worker can recover deterministically after
-- crashes, retries, DLQ round-trips, or post-soft-delete processing.

ALTER TABLE deleted_libraries
	ADD IF NOT EXISTS block_representation_id TEXT;

ALTER TABLE gc_queue
	ADD IF NOT EXISTS block_representation_id TEXT;

ALTER TABLE gc_failed_items
	ADD IF NOT EXISTS block_representation_id TEXT;
